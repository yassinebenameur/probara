package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/yassinebenameur/probara/shared/models"
)

var syntheticTemplateRegex = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.-]+)\s*\}\}`)

// SyntheticAPIChecker implements workflow-style API monitoring.
type SyntheticAPIChecker struct{}

// NewSyntheticAPIChecker creates a new synthetic API checker.
func NewSyntheticAPIChecker() *SyntheticAPIChecker {
	return &SyntheticAPIChecker{}
}

type syntheticAPIMetricsEnvelope struct {
	SyntheticAPI *syntheticAPIMetrics `json:"synthetic_api,omitempty"`
}

type syntheticAPIMetrics struct {
	FailureMode    string                    `json:"failure_mode"`
	CompletedSteps int                       `json:"completed_steps"`
	TotalLatencyMs int64                     `json:"total_latency_ms"`
	FailedStepID   *string                   `json:"failed_step_id,omitempty"`
	Steps          []syntheticAPIStepMetrics `json:"steps"`
}

type syntheticAPIStepMetrics struct {
	ID               string            `json:"id"`
	Name             string            `json:"name,omitempty"`
	Status           string            `json:"status"`
	Method           string            `json:"method"`
	URL              string            `json:"url"`
	HTTPStatus       *int              `json:"http_status,omitempty"`
	LatencyMs        int64             `json:"latency_ms"`
	Error            *string           `json:"error,omitempty"`
	AssertionsFailed []string          `json:"assertions_failed,omitempty"`
	Extracted        map[string]string `json:"extracted,omitempty"`
}

// Check executes a synthetic API journey.
func (c *SyntheticAPIChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.SyntheticAPIMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		errMsg := fmt.Sprintf("failed to unmarshal synthetic_api config: %v", err)
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	if len(config.Steps) == 0 {
		errMsg := "synthetic_api config must include at least one step"
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	failureMode := strings.ToLower(strings.TrimSpace(config.FailureMode))
	if failureMode == "" {
		failureMode = "fail_fast"
	}

	var baseURL *url.URL
	if strings.TrimSpace(config.BaseURL) != "" {
		parsed, err := url.Parse(config.BaseURL)
		if err != nil {
			errMsg := fmt.Sprintf("invalid base_url: %v", err)
			return CheckResult{Status: "error", ErrorMessage: &errMsg}
		}
		baseURL = parsed
	}

	vars := make(map[string]string, len(config.Variables))
	for k, v := range config.Variables {
		vars[k] = v
	}

	start := time.Now()
	overallStatus := "success"
	var summaryError string
	var lastHTTPStatus *int
	var failedStepID *string
	stepMetrics := make([]syntheticAPIStepMetrics, 0, len(config.Steps))

	for _, step := range config.Steps {
		stepStart := time.Now()
		stepResult := syntheticAPIStepMetrics{
			ID:     step.ID,
			Name:   step.Name,
			Status: "success",
		}

		method := strings.ToUpper(strings.TrimSpace(step.Request.Method))
		if method == "" {
			method = "GET"
		}
		stepResult.Method = method

		resolvedURL, err := resolveSyntheticStepURL(baseURL, step.Request.URL, vars)
		if err != nil {
			msg := fmt.Sprintf("step %s url error: %v", step.ID, err)
			stepResult.Status = "error"
			stepResult.Error = &msg
			stepResult.LatencyMs = time.Since(stepStart).Milliseconds()
			stepMetrics = append(stepMetrics, stepResult)
			if summaryError == "" {
				summaryError = msg
			}
			if failedStepID == nil {
				failedStepID = &step.ID
			}
			overallStatus = "error"
			if failureMode == "fail_fast" {
				break
			}
			continue
		}
		stepResult.URL = resolvedURL

		body, err := interpolateOptionalTemplate(step.Request.Body, vars)
		if err != nil {
			msg := fmt.Sprintf("step %s body interpolation error: %v", step.ID, err)
			stepResult.Status = "error"
			stepResult.Error = &msg
			stepResult.LatencyMs = time.Since(stepStart).Milliseconds()
			stepMetrics = append(stepMetrics, stepResult)
			if summaryError == "" {
				summaryError = msg
			}
			if failedStepID == nil {
				failedStepID = &step.ID
			}
			overallStatus = "error"
			if failureMode == "fail_fast" {
				break
			}
			continue
		}

		headers := make(map[string]string, len(step.Request.Headers))
		for k, v := range step.Request.Headers {
			resolved, err := interpolateTemplate(v, vars)
			if err != nil {
				msg := fmt.Sprintf("step %s header interpolation error: %v", step.ID, err)
				stepResult.Status = "error"
				stepResult.Error = &msg
				stepResult.LatencyMs = time.Since(stepStart).Milliseconds()
				stepMetrics = append(stepMetrics, stepResult)
				if summaryError == "" {
					summaryError = msg
				}
				if failedStepID == nil {
					failedStepID = &step.ID
				}
				overallStatus = "error"
				if failureMode == "fail_fast" {
					break
				}
				break
			}
			headers[k] = resolved
		}
		if stepResult.Status == "error" {
			if failureMode == "fail_fast" {
				break
			}
			continue
		}

		stepTimeout := timeoutSeconds
		if step.Request.TimeoutSeconds != nil && *step.Request.TimeoutSeconds > 0 {
			stepTimeout = *step.Request.TimeoutSeconds
		}
		stepCtx, cancel := context.WithTimeout(ctx, time.Duration(stepTimeout)*time.Second)

		var bodyReader io.Reader
		if body != nil {
			bodyReader = strings.NewReader(*body)
		}

		req, err := http.NewRequestWithContext(stepCtx, method, resolvedURL, bodyReader)
		if err != nil {
			cancel()
			msg := fmt.Sprintf("step %s request creation failed: %v", step.ID, err)
			stepResult.Status = "error"
			stepResult.Error = &msg
			stepResult.LatencyMs = time.Since(stepStart).Milliseconds()
			stepMetrics = append(stepMetrics, stepResult)
			if summaryError == "" {
				summaryError = msg
			}
			if failedStepID == nil {
				failedStepID = &step.ID
			}
			overallStatus = "error"
			if failureMode == "fail_fast" {
				break
			}
			continue
		}

		for k, v := range headers {
			req.Header.Set(k, v)
		}

		client := &http.Client{
			Timeout: time.Duration(stepTimeout) * time.Second,
		}
		followRedirects := true
		if step.Request.FollowRedirects != nil {
			followRedirects = *step.Request.FollowRedirects
		}
		maxRedirects := 10
		if step.Request.MaxRedirects != nil {
			maxRedirects = *step.Request.MaxRedirects
		}
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if !followRedirects {
				return http.ErrUseLastResponse
			}
			if maxRedirects >= 0 && len(via) > maxRedirects {
				return fmt.Errorf("redirects: stopped after %d redirects", maxRedirects)
			}
			return nil
		}

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			msg := fmt.Sprintf("step %s request failed: %v", step.ID, err)
			stepResult.Status = "error"
			stepResult.Error = &msg
			stepResult.LatencyMs = time.Since(stepStart).Milliseconds()
			stepMetrics = append(stepMetrics, stepResult)
			if summaryError == "" {
				summaryError = msg
			}
			if failedStepID == nil {
				failedStepID = &step.ID
			}
			overallStatus = "error"
			if failureMode == "fail_fast" {
				break
			}
			continue
		}

		func() {
			defer resp.Body.Close()
			defer cancel()

			stepResult.HTTPStatus = &resp.StatusCode
			lastHTTPStatus = &resp.StatusCode

			payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 1_048_576))
			if readErr != nil {
				msg := fmt.Sprintf("step %s failed to read response body: %v", step.ID, readErr)
				stepResult.Status = "error"
				stepResult.Error = &msg
				if summaryError == "" {
					summaryError = msg
				}
				if failedStepID == nil {
					failedStepID = &step.ID
				}
				overallStatus = "error"
				return
			}
			bodyText := string(payload)

			assertionFailures, evalErr := evaluateSyntheticAssertions(step.Assert, resp, bodyText)
			if evalErr != nil {
				msg := fmt.Sprintf("step %s assertion error: %v", step.ID, evalErr)
				stepResult.Status = "error"
				stepResult.Error = &msg
				if summaryError == "" {
					summaryError = msg
				}
				if failedStepID == nil {
					failedStepID = &step.ID
				}
				overallStatus = "error"
				return
			}
			if len(assertionFailures) > 0 {
				stepResult.Status = "failure"
				stepResult.AssertionsFailed = assertionFailures
				msg := strings.Join(assertionFailures, "; ")
				stepResult.Error = &msg
				if summaryError == "" {
					summaryError = msg
				}
				if failedStepID == nil {
					failedStepID = &step.ID
				}
				if overallStatus != "error" {
					overallStatus = "failure"
				}
			}

			extracted, extractErr := extractSyntheticVariables(step.Extract, resp, bodyText, vars)
			if extractErr != nil {
				msg := fmt.Sprintf("step %s extraction error: %v", step.ID, extractErr)
				stepResult.Status = "error"
				stepResult.Error = &msg
				if summaryError == "" {
					summaryError = msg
				}
				if failedStepID == nil {
					failedStepID = &step.ID
				}
				overallStatus = "error"
				return
			}
			if len(extracted) > 0 {
				stepResult.Extracted = extracted
			}
		}()

		stepResult.LatencyMs = time.Since(stepStart).Milliseconds()
		stepMetrics = append(stepMetrics, stepResult)

		if stepResult.Status != "success" && failureMode == "fail_fast" {
			break
		}
	}

	totalLatency := time.Since(start).Milliseconds()
	metricsData, _ := json.Marshal(syntheticAPIMetricsEnvelope{
		SyntheticAPI: &syntheticAPIMetrics{
			FailureMode:    failureMode,
			CompletedSteps: len(stepMetrics),
			TotalLatencyMs: totalLatency,
			FailedStepID:   failedStepID,
			Steps:          stepMetrics,
		},
	})

	var errPtr *string
	if summaryError != "" {
		errPtr = &summaryError
	}

	return CheckResult{
		Status:       overallStatus,
		HTTPStatus:   lastHTTPStatus,
		LatencyMs:    &totalLatency,
		ErrorMessage: errPtr,
		MetricsData:  metricsData,
	}
}

func resolveSyntheticStepURL(baseURL *url.URL, raw string, vars map[string]string) (string, error) {
	resolved, err := interpolateTemplate(raw, vars)
	if err != nil {
		return "", err
	}

	parsed, err := url.Parse(strings.TrimSpace(resolved))
	if err != nil {
		return "", err
	}
	if parsed.IsAbs() {
		return parsed.String(), nil
	}
	if baseURL == nil {
		return "", fmt.Errorf("relative URL requires base_url")
	}
	return baseURL.ResolveReference(parsed).String(), nil
}

func interpolateOptionalTemplate(value *string, vars map[string]string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	resolved, err := interpolateTemplate(*value, vars)
	if err != nil {
		return nil, err
	}
	return &resolved, nil
}

func interpolateTemplate(input string, vars map[string]string) (string, error) {
	var missing string
	resolved := syntheticTemplateRegex.ReplaceAllStringFunc(input, func(token string) string {
		matches := syntheticTemplateRegex.FindStringSubmatch(token)
		if len(matches) < 2 {
			return token
		}
		key := strings.TrimSpace(matches[1])
		val, ok := vars[key]
		if !ok {
			missing = key
			return token
		}
		return val
	})
	if missing != "" {
		return "", fmt.Errorf("missing variable %q", missing)
	}
	return resolved, nil
}

func evaluateSyntheticAssertions(assertions []models.SyntheticAPIAssertionConfig, resp *http.Response, body string) ([]string, error) {
	failures := make([]string, 0)

	for i, assertion := range assertions {
		target := strings.ToLower(strings.TrimSpace(assertion.Target))
		op := strings.ToLower(strings.TrimSpace(assertion.Op))

		var expected interface{}
		if len(assertion.Value) > 0 {
			if err := json.Unmarshal(assertion.Value, &expected); err != nil {
				return nil, fmt.Errorf("assert[%d] invalid value: %w", i, err)
			}
		}

		switch target {
		case "status":
			ok, err := evaluateSyntheticStatusAssertion(resp.StatusCode, op, expected)
			if err != nil {
				return nil, fmt.Errorf("assert[%d] status: %w", i, err)
			}
			if !ok {
				failures = append(failures, fmt.Sprintf("status assertion failed: op=%s expected=%v actual=%d", op, expected, resp.StatusCode))
			}
		case "header":
			headerName := strings.TrimSpace(assertion.Path)
			actual := resp.Header.Get(headerName)
			ok, err := evaluateSyntheticStringAssertion(actual, op, expected)
			if err != nil {
				return nil, fmt.Errorf("assert[%d] header: %w", i, err)
			}
			if !ok {
				failures = append(failures, fmt.Sprintf("header assertion failed: header=%s op=%s expected=%v actual=%q", headerName, op, expected, actual))
			}
		case "body":
			ok, err := evaluateSyntheticStringAssertion(body, op, expected)
			if err != nil {
				return nil, fmt.Errorf("assert[%d] body: %w", i, err)
			}
			if !ok {
				failures = append(failures, fmt.Sprintf("body assertion failed: op=%s expected=%v", op, expected))
			}
		case "json":
			ok, err := evaluateSyntheticJSONAssertion(body, assertion.Path, op, expected)
			if err != nil {
				return nil, fmt.Errorf("assert[%d] json: %w", i, err)
			}
			if !ok {
				failures = append(failures, fmt.Sprintf("json assertion failed: path=%s op=%s expected=%v", assertion.Path, op, expected))
			}
		default:
			return nil, fmt.Errorf("unsupported target %q", target)
		}
	}

	return failures, nil
}

func evaluateSyntheticStatusAssertion(actual int, op string, expected interface{}) (bool, error) {
	switch op {
	case "equals":
		v, err := toInt(expected)
		if err != nil {
			return false, err
		}
		return actual == v, nil
	case "not_equals":
		v, err := toInt(expected)
		if err != nil {
			return false, err
		}
		return actual != v, nil
	case "in":
		values, ok := expected.([]interface{})
		if !ok || len(values) == 0 {
			return false, fmt.Errorf("expected a non-empty array for op=in")
		}
		for _, item := range values {
			v, err := toInt(item)
			if err != nil {
				return false, err
			}
			if actual == v {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("unsupported op %q for status target", op)
	}
}

func evaluateSyntheticStringAssertion(actual, op string, expected interface{}) (bool, error) {
	switch op {
	case "exists":
		return strings.TrimSpace(actual) != "", nil
	case "equals":
		return actual == fmt.Sprint(expected), nil
	case "not_equals":
		return actual != fmt.Sprint(expected), nil
	case "contains":
		return strings.Contains(actual, fmt.Sprint(expected)), nil
	case "not_contains":
		return !strings.Contains(actual, fmt.Sprint(expected)), nil
	case "regex":
		re, err := regexp.Compile(fmt.Sprint(expected))
		if err != nil {
			return false, err
		}
		return re.MatchString(actual), nil
	case "not_regex":
		re, err := regexp.Compile(fmt.Sprint(expected))
		if err != nil {
			return false, err
		}
		return !re.MatchString(actual), nil
	default:
		return false, fmt.Errorf("unsupported op %q for string target", op)
	}
}

func evaluateSyntheticJSONAssertion(body, path, op string, expected interface{}) (bool, error) {
	if !gjson.Valid(body) {
		return false, fmt.Errorf("response body is not valid JSON")
	}

	result := gjson.Get(body, path)

	switch op {
	case "exists":
		return result.Exists(), nil
	case "equals":
		return syntheticJSONEquals(result, expected), nil
	case "not_equals":
		return !syntheticJSONEquals(result, expected), nil
	case "contains":
		return strings.Contains(result.String(), fmt.Sprint(expected)), nil
	case "not_contains":
		return !strings.Contains(result.String(), fmt.Sprint(expected)), nil
	case "regex":
		re, err := regexp.Compile(fmt.Sprint(expected))
		if err != nil {
			return false, err
		}
		return re.MatchString(result.String()), nil
	case "not_regex":
		re, err := regexp.Compile(fmt.Sprint(expected))
		if err != nil {
			return false, err
		}
		return !re.MatchString(result.String()), nil
	case "number_gt":
		v, err := toFloat(expected)
		if err != nil {
			return false, err
		}
		return result.Float() > v, nil
	case "number_gte":
		v, err := toFloat(expected)
		if err != nil {
			return false, err
		}
		return result.Float() >= v, nil
	case "number_lt":
		v, err := toFloat(expected)
		if err != nil {
			return false, err
		}
		return result.Float() < v, nil
	case "number_lte":
		v, err := toFloat(expected)
		if err != nil {
			return false, err
		}
		return result.Float() <= v, nil
	case "bool_is":
		v, err := toBool(expected)
		if err != nil {
			return false, err
		}
		return result.Bool() == v, nil
	default:
		return false, fmt.Errorf("unsupported op %q for json target", op)
	}
}

func extractSyntheticVariables(
	extractors []models.SyntheticAPIExtractConfig,
	resp *http.Response,
	body string,
	vars map[string]string,
) (map[string]string, error) {
	if len(extractors) == 0 {
		return nil, nil
	}

	extracted := make(map[string]string, len(extractors))
	for i, extractor := range extractors {
		name := strings.TrimSpace(extractor.Name)
		from := strings.ToLower(strings.TrimSpace(extractor.From))
		path := strings.TrimSpace(extractor.Path)

		var value string
		switch from {
		case "json":
			if !gjson.Valid(body) {
				return nil, fmt.Errorf("extract[%d]: response body is not valid JSON", i)
			}
			res := gjson.Get(body, path)
			if !res.Exists() {
				return nil, fmt.Errorf("extract[%d]: json path %q not found", i, path)
			}
			value = res.String()
		case "header":
			value = resp.Header.Get(path)
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("extract[%d]: header %q not found", i, path)
			}
		default:
			return nil, fmt.Errorf("extract[%d]: unsupported source %q", i, from)
		}

		vars[name] = value
		if extractor.Sensitive != nil && *extractor.Sensitive {
			extracted[name] = "[REDACTED]"
		} else {
			extracted[name] = value
		}
	}

	return extracted, nil
}

func syntheticJSONEquals(actual gjson.Result, expected interface{}) bool {
	switch v := expected.(type) {
	case bool:
		return actual.Bool() == v
	case float64:
		return actual.Float() == v
	case string:
		return actual.String() == v
	default:
		return actual.String() == fmt.Sprint(expected)
	}
}

func toInt(value interface{}) (int, error) {
	switch v := value.(type) {
	case float64:
		if v != math.Trunc(v) {
			return 0, fmt.Errorf("not an int value")
		}
		return int(v), nil
	case int:
		return v, nil
	case string:
		return strconv.Atoi(strings.TrimSpace(v))
	default:
		return 0, fmt.Errorf("not an int value")
	}
}

func toFloat(value interface{}) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(v), 64)
	default:
		return 0, fmt.Errorf("not a float value")
	}
}

func toBool(value interface{}) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		return strconv.ParseBool(strings.TrimSpace(strings.ToLower(v)))
	default:
		return false, fmt.Errorf("not a bool value")
	}
}
