// Parsing: format autodetect and the JSON, YAML, CSV and portable-export readers that turn an upload into rows.

package importservice

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// ParseFile auto-detects the file format and parses its contents
func (s *Service) ParseFile(data []byte, filename string) (*models.ImportPreviewResponse, error) {
	ext := strings.ToLower(filepath.Ext(filename))

	var format models.ImportFormat
	var src *parsedSource
	var err error

	// Try to detect format from extension first
	switch ext {
	case ".json":
		format = models.ImportFormatJSON
		src, err = s.parseJSON(data)
	case ".yaml", ".yml":
		format = models.ImportFormatYAML
		src, err = s.parseYAML(data)
	case ".csv":
		format = models.ImportFormatCSV
		src, err = s.parseCSV(data)
	default:
		// Try to auto-detect from content
		format, src, err = s.autoDetectAndParse(data)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to parse file: %w", err)
	}

	// A source that translated to nothing but explained why is a valid answer:
	// surface the reasons instead of a bare "no data found".
	if len(src.Rows) == 0 {
		if len(src.SkippedRows) > 0 {
			return nil, fmt.Errorf("no monitors could be imported: %s", summarizeSkipReasons(src.SkippedRows))
		}
		return nil, fmt.Errorf("no data found in file")
	}

	// Extract all unique field names
	fieldSet := make(map[string]bool)
	for _, row := range src.Rows {
		for key := range row.Fields {
			fieldSet[key] = true
		}
	}

	detectedFields := make([]string, 0, len(fieldSet))
	for field := range fieldSet {
		detectedFields = append(detectedFields, field)
	}

	// Generate suggested mapping
	suggestedMapping := s.suggestMapping(detectedFields)

	// Generate warnings (ensure non-nil). Adapter warnings come first: they
	// describe the translation the operator is about to accept.
	warnings := append([]string{}, src.Warnings...)
	warnings = append(warnings, s.generateWarnings(src.Rows, suggestedMapping)...)

	// Detect types and suggest mappings
	detectedTypes, suggestedTypeMapping := s.detectAndSuggestTypes(src.Rows, suggestedMapping)

	return &models.ImportPreviewResponse{
		Format:               format,
		Schema:               src.Schema,
		Rows:                 src.Rows,
		DetectedFields:       detectedFields,
		SuggestedMapping:     suggestedMapping,
		Warnings:             warnings,
		TotalRows:            len(src.Rows),
		DetectedTypes:        detectedTypes,
		SuggestedTypeMapping: suggestedTypeMapping,
		SkippedRows:          src.SkippedRows,
	}, nil
}

// summarizeSkipReasons collapses skipped rows into one sentence, counting each
// distinct reason so a 40-monitor file does not produce 40 identical clauses.
func summarizeSkipReasons(skipped []models.ImportSkippedRow) string {
	counts := make(map[string]int)
	order := make([]string, 0, len(skipped))
	for _, row := range skipped {
		if _, seen := counts[row.Reason]; !seen {
			order = append(order, row.Reason)
		}
		counts[row.Reason]++
	}

	parts := make([]string, 0, len(order))
	for _, reason := range order {
		if counts[reason] > 1 {
			parts = append(parts, fmt.Sprintf("%s (%d)", reason, counts[reason]))
			continue
		}
		parts = append(parts, reason)
	}
	return strings.Join(parts, "; ")
}

// parseJSON parses JSON data
func (s *Service) parseJSON(data []byte) (*parsedSource, error) {
	// Known foreign schemas are recognized before the generic shapes, which
	// would otherwise flatten an Uptime Kuma backup into a single junk row.
	if src, ok, err := s.parseKumaExport(data); err != nil {
		return nil, err
	} else if ok {
		return src, nil
	}

	// Try to parse as array first
	var arrayData []map[string]interface{}
	if err := json.Unmarshal(data, &arrayData); err == nil {
		return &parsedSource{Rows: s.mapsToRows(arrayData)}, nil
	}

	// Try to parse as object with items array
	var objectData struct {
		Items    []map[string]interface{} `json:"items"`
		Monitors []map[string]interface{} `json:"monitors"`
		Data     []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(data, &objectData); err == nil {
		if len(objectData.Items) > 0 {
			return &parsedSource{Rows: s.mapsToRows(objectData.Items)}, nil
		}
		if len(objectData.Monitors) > 0 {
			return &parsedSource{Rows: s.mapsToRows(objectData.Monitors)}, nil
		}
		if len(objectData.Data) > 0 {
			return &parsedSource{Rows: s.mapsToRows(objectData.Data)}, nil
		}
	}

	// Try to parse as single object
	var singleData map[string]interface{}
	if err := json.Unmarshal(data, &singleData); err == nil {
		return &parsedSource{Rows: s.mapsToRows([]map[string]interface{}{singleData})}, nil
	}

	return nil, fmt.Errorf("invalid JSON format")
}

// parseYAML parses YAML data
func (s *Service) parseYAML(data []byte) (*parsedSource, error) {
	if rows, ok, err := s.parsePortableExport(data); err != nil {
		return nil, err
	} else if ok {
		return &parsedSource{Rows: rows, Schema: models.ImportSchemaPortableMonitorExport}, nil
	}

	// First, try to parse as a generic interface to see what we have
	var generic interface{}
	if err := yaml.Unmarshal(data, &generic); err != nil {
		return nil, fmt.Errorf("invalid YAML: %v", err)
	}

	// Handle based on type
	switch v := generic.(type) {
	case []interface{}:
		// It's an array - convert each item to map
		maps := make([]map[string]interface{}, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				maps = append(maps, m)
			}
		}
		if len(maps) > 0 {
			return &parsedSource{Rows: s.mapsToRows(maps)}, nil
		}
		return nil, fmt.Errorf("YAML array contains no valid objects")

	case map[string]interface{}:
		// It's an object - check for common wrapper keys
		for _, key := range []string{"items", "monitors", "data", "services", "endpoints", "checks"} {
			if items, ok := v[key]; ok {
				if itemArray, ok := items.([]interface{}); ok {
					maps := make([]map[string]interface{}, 0, len(itemArray))
					for _, item := range itemArray {
						if m, ok := item.(map[string]interface{}); ok {
							maps = append(maps, m)
						}
					}
					if len(maps) > 0 {
						return &parsedSource{Rows: s.mapsToRows(maps)}, nil
					}
				}
			}
		}
		// Treat as single monitor
		if len(v) > 0 {
			return &parsedSource{Rows: s.mapsToRows([]map[string]interface{}{v})}, nil
		}
		return nil, fmt.Errorf("YAML object is empty")

	default:
		return nil, fmt.Errorf("YAML must be an array or object, got %T", generic)
	}
}

func (s *Service) parsePortableExport(data []byte) ([]models.ImportRow, bool, error) {
	var bundle models.PortableMonitorExport
	if err := yaml.Unmarshal(data, &bundle); err != nil {
		return nil, false, nil
	}

	if bundle.Kind != "monitor_export" || bundle.Version != 1 {
		return nil, false, nil
	}

	rows := make([]models.ImportRow, 0, len(bundle.Monitors))
	for i, monitor := range bundle.Monitors {
		fields := map[string]interface{}{
			"name":             monitor.Name,
			"type":             string(monitor.Type),
			"config":           monitor.Config,
			"interval_seconds": monitor.IntervalSeconds,
			"timeout_seconds":  monitor.TimeoutSeconds,
			"enabled":          monitor.Enabled,
		}
		if len(monitor.Tags) > 0 {
			fields["tags"] = monitor.Tags
		}
		if len(monitor.GroupMembers) > 0 {
			fields["group_members"] = monitor.GroupMembers
		}
		if monitor.ConsecutiveFailuresThreshold > 0 {
			fields["consecutive_failures_threshold"] = monitor.ConsecutiveFailuresThreshold
		}

		rows = append(rows, models.ImportRow{
			Index:  i,
			Fields: fields,
		})
	}

	return rows, true, nil
}

// parseCSV parses CSV data
func (s *Service) parseCSV(data []byte) (*parsedSource, error) {
	reader := csv.NewReader(bytes.NewReader(data))

	// Read header row
	headers, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV header: %w", err)
	}

	// Read all records
	var rows []models.ImportRow
	index := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read CSV row %d: %w", index+1, err)
		}

		fields := make(map[string]interface{})
		for i, header := range headers {
			if i < len(record) {
				// Try to parse as number or bool
				fields[header] = s.parseValue(record[i])
			}
		}

		rows = append(rows, models.ImportRow{
			Index:  index,
			Fields: fields,
		})
		index++
	}

	return &parsedSource{Rows: rows}, nil
}

// autoDetectAndParse tries to auto-detect format from content
func (s *Service) autoDetectAndParse(data []byte) (models.ImportFormat, *parsedSource, error) {
	// Try JSON first
	src, err := s.parseJSON(data)
	if err == nil {
		return models.ImportFormatJSON, src, nil
	}

	// Try YAML
	src, err = s.parseYAML(data)
	if err == nil {
		return models.ImportFormatYAML, src, nil
	}

	// Try CSV
	src, err = s.parseCSV(data)
	if err == nil {
		return models.ImportFormatCSV, src, nil
	}

	return "", nil, fmt.Errorf("could not detect file format")
}

// mapsToRows converts a slice of maps to ImportRows
func (s *Service) mapsToRows(maps []map[string]interface{}) []models.ImportRow {
	rows := make([]models.ImportRow, len(maps))
	for i, m := range maps {
		// Flatten nested objects
		flattened := s.flattenMap(m, "")
		rows[i] = models.ImportRow{
			Index:  i,
			Fields: flattened,
		}
	}
	return rows
}

// flattenMap flattens nested maps with dot notation
func (s *Service) flattenMap(m map[string]interface{}, prefix string) map[string]interface{} {
	result := make(map[string]interface{})
	for key, value := range m {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		switch v := value.(type) {
		case map[string]interface{}:
			// Recursively flatten
			for k, val := range s.flattenMap(v, fullKey) {
				result[k] = val
			}
		default:
			result[fullKey] = value
		}
	}
	return result
}

// parseValue tries to parse a string value to appropriate type
func (s *Service) parseValue(val string) interface{} {
	val = strings.TrimSpace(val)

	// Try bool
	if strings.EqualFold(val, "true") {
		return true
	}
	if strings.EqualFold(val, "false") {
		return false
	}

	// Try int
	if i, err := strconv.Atoi(val); err == nil {
		return i
	}

	// Try float
	if f, err := strconv.ParseFloat(val, 64); err == nil {
		return f
	}

	return val
}
