package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/yassinebenameur/probara/shared/models"
)

type syntheticBrowserMetricsEnvelope struct {
	SyntheticBrowser *syntheticBrowserMetrics `json:"synthetic_browser,omitempty"`
}

type syntheticBrowserMetrics struct {
	FailureMode    string                           `json:"failure_mode"`
	StartURL       string                           `json:"start_url"`
	Device         string                           `json:"device,omitempty"`
	CompletedSteps int                              `json:"completed_steps"`
	TotalLatencyMs int64                            `json:"total_latency_ms"`
	FinalURL       string                           `json:"final_url,omitempty"`
	FailedStepID   *string                          `json:"failed_step_id,omitempty"`
	Steps          []syntheticBrowserStepMetrics    `json:"steps"`
	Artifacts      *syntheticBrowserArtifactMetrics `json:"artifacts,omitempty"`
	Warnings       []string                         `json:"warnings,omitempty"`
}

type syntheticBrowserStepMetrics struct {
	ID        string  `json:"id"`
	Action    string  `json:"action"`
	Status    string  `json:"status"`
	URL       string  `json:"url,omitempty"`
	Selector  string  `json:"selector,omitempty"`
	LatencyMs int64   `json:"latency_ms"`
	Error     *string `json:"error,omitempty"`
}

type syntheticBrowserArtifactMetrics struct {
	ScreenshotPath *string  `json:"screenshot_path,omitempty"`
	TracePath      *string  `json:"trace_path,omitempty"`
	HARPath        *string  `json:"har_path,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
}

func (m *syntheticBrowserArtifactMetrics) hasData() bool {
	return m != nil &&
		(m.ScreenshotPath != nil || m.TracePath != nil || m.HARPath != nil || len(m.Warnings) > 0)
}

type syntheticBrowserStepFailure struct {
	message string
}

func (e *syntheticBrowserStepFailure) Error() string {
	return e.message
}

func newSyntheticBrowserStepFailure(format string, args ...interface{}) error {
	return &syntheticBrowserStepFailure{message: fmt.Sprintf(format, args...)}
}

// SyntheticBrowserChecker runs synthetic browser steps in a headless Chromium session.
type SyntheticBrowserChecker struct {
	artifactsDir string
}

// NewSyntheticBrowserChecker creates a new synthetic browser checker.
func NewSyntheticBrowserChecker(artifactsDir string) *SyntheticBrowserChecker {
	dir := strings.TrimSpace(artifactsDir)
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "probara", "synthetic-browser-artifacts")
	}
	return &SyntheticBrowserChecker{artifactsDir: dir}
}

// Check executes a synthetic browser journey.
func (c *SyntheticBrowserChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.SyntheticBrowserMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		errMsg := fmt.Sprintf("failed to unmarshal synthetic_browser config: %v", err)
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	if strings.TrimSpace(config.StartURL) == "" {
		errMsg := "synthetic_browser config must include start_url"
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}
	if len(config.Steps) == 0 {
		errMsg := "synthetic_browser config must include at least one step"
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	failureMode := strings.ToLower(strings.TrimSpace(config.FailureMode))
	if failureMode == "" {
		failureMode = "fail_fast"
	}

	vars := make(map[string]string, len(config.Variables))
	for key, value := range config.Variables {
		vars[key] = value
	}

	resolvedStartURL, err := interpolateTemplate(config.StartURL, vars)
	if err != nil {
		errMsg := fmt.Sprintf("start_url interpolation error: %v", err)
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}
	parsedStartURL, err := url.Parse(strings.TrimSpace(resolvedStartURL))
	if err != nil {
		errMsg := fmt.Sprintf("invalid start_url: %v", err)
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}
	if !parsedStartURL.IsAbs() {
		errMsg := "start_url must be absolute"
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	browserPath := findBrowserExecutable()
	if browserPath == "" {
		errMsg := "no Chromium/Chrome executable found; set CHROME_BIN in worker environment"
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	startedAt := time.Now()
	overallStatus := "success"
	var summaryError string
	var failedStepID *string
	currentURL := resolvedStartURL
	stepMetrics := make([]syntheticBrowserStepMetrics, 0, len(config.Steps))
	journeyWarnings := make([]string, 0, 2)
	artifactMetrics := &syntheticBrowserArtifactMetrics{}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(
		ctx,
		append(
			chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(browserPath),
			chromedp.Flag("headless", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-dev-shm-usage", true),
			chromedp.Flag("disable-gpu", true),
		)...,
	)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	runCtx, cancelRun := context.WithTimeout(browserCtx, time.Duration(timeoutSeconds)*time.Second)
	defer cancelRun()

	if err := chromedp.Run(runCtx,
		chromedp.Navigate(resolvedStartURL),
		chromedp.Location(&currentURL),
	); err != nil {
		errMsg := fmt.Sprintf("failed to open start_url: %v", err)
		totalLatency := time.Since(startedAt).Milliseconds()
		metricsData, _ := json.Marshal(syntheticBrowserMetricsEnvelope{
			SyntheticBrowser: &syntheticBrowserMetrics{
				FailureMode:    failureMode,
				StartURL:       resolvedStartURL,
				Device:         strings.TrimSpace(config.Device),
				CompletedSteps: 0,
				TotalLatencyMs: totalLatency,
				FinalURL:       currentURL,
				Steps:          stepMetrics,
			},
		})
		return CheckResult{
			Status:       "error",
			LatencyMs:    &totalLatency,
			ErrorMessage: &errMsg,
			MetricsData:  metricsData,
		}
	}

	for _, step := range config.Steps {
		stepStartedAt := time.Now()
		stepAction := strings.ToLower(strings.TrimSpace(step.Action))
		stepResult := syntheticBrowserStepMetrics{
			ID:     step.ID,
			Action: stepAction,
			Status: "success",
		}

		stepTimeout := timeoutSeconds
		if step.TimeoutSeconds != nil && *step.TimeoutSeconds > 0 {
			stepTimeout = *step.TimeoutSeconds
		}
		stepCtx, cancelStep := context.WithTimeout(runCtx, time.Duration(stepTimeout)*time.Second)

		runErr := executeSyntheticBrowserStep(stepCtx, step, vars, resolvedStartURL, &currentURL, &stepResult)
		cancelStep()

		stepResult.LatencyMs = time.Since(stepStartedAt).Milliseconds()
		if runErr != nil {
			var stepFailure *syntheticBrowserStepFailure
			if errors.As(runErr, &stepFailure) {
				stepResult.Status = "failure"
			} else {
				stepResult.Status = "error"
			}

			msg := runErr.Error()
			stepResult.Error = &msg
			if summaryError == "" {
				summaryError = msg
			}
			if failedStepID == nil {
				stepID := step.ID
				failedStepID = &stepID
			}
			if stepResult.Status == "error" {
				overallStatus = "error"
			} else if overallStatus != "error" {
				overallStatus = "failure"
			}
		}

		stepMetrics = append(stepMetrics, stepResult)
		if stepResult.Status != "success" && failureMode == "fail_fast" {
			break
		}
	}

	if overallStatus != "success" {
		if config.Artifacts.ScreenshotOnFailure {
			screenshotPath, screenshotErr := c.captureScreenshotWithFallback(browserCtx, allocCtx, currentURL)
			if screenshotErr != nil {
				artifactMetrics.Warnings = append(artifactMetrics.Warnings, fmt.Sprintf("screenshot capture failed: %v", screenshotErr))
			} else {
				artifactMetrics.ScreenshotPath = &screenshotPath
			}
		}
		if config.Artifacts.TraceOnFailure {
			artifactMetrics.Warnings = append(artifactMetrics.Warnings, "trace_on_failure is not supported by Chromium CDP runner yet")
		}
		if config.Artifacts.HarOnFailure {
			artifactMetrics.Warnings = append(artifactMetrics.Warnings, "har_on_failure is not supported by Chromium CDP runner yet")
		}
	}

	if strings.TrimSpace(config.Device) != "" && !strings.EqualFold(strings.TrimSpace(config.Device), "Desktop Chrome") {
		journeyWarnings = append(journeyWarnings, "custom device emulation is not supported yet; using default desktop Chrome")
	}

	totalLatency := time.Since(startedAt).Milliseconds()
	metricsPayload := &syntheticBrowserMetrics{
		FailureMode:    failureMode,
		StartURL:       resolvedStartURL,
		Device:         strings.TrimSpace(config.Device),
		CompletedSteps: len(stepMetrics),
		TotalLatencyMs: totalLatency,
		FinalURL:       currentURL,
		FailedStepID:   failedStepID,
		Steps:          stepMetrics,
		Warnings:       journeyWarnings,
	}
	if artifactMetrics.hasData() {
		metricsPayload.Artifacts = artifactMetrics
	}

	metricsData, _ := json.Marshal(syntheticBrowserMetricsEnvelope{
		SyntheticBrowser: metricsPayload,
	})

	var errPtr *string
	if summaryError != "" {
		errPtr = &summaryError
	}

	return CheckResult{
		Status:       overallStatus,
		LatencyMs:    &totalLatency,
		ErrorMessage: errPtr,
		MetricsData:  metricsData,
	}
}

func executeSyntheticBrowserStep(
	ctx context.Context,
	step models.SyntheticBrowserStepConfig,
	vars map[string]string,
	startURL string,
	currentURL *string,
	stepResult *syntheticBrowserStepMetrics,
) error {
	action := strings.ToLower(strings.TrimSpace(step.Action))

	resolvedURL := strings.TrimSpace(step.URL)
	if resolvedURL != "" {
		interpolated, err := interpolateTemplate(resolvedURL, vars)
		if err != nil {
			return fmt.Errorf("step %s url interpolation error: %w", step.ID, err)
		}
		resolvedURL = strings.TrimSpace(interpolated)
	}

	resolvedSelector := strings.TrimSpace(step.Selector)
	if resolvedSelector != "" {
		interpolated, err := interpolateTemplate(resolvedSelector, vars)
		if err != nil {
			return fmt.Errorf("step %s selector interpolation error: %w", step.ID, err)
		}
		resolvedSelector = strings.TrimSpace(interpolated)
	}

	var resolvedValue string
	if step.Value != nil {
		interpolated, err := interpolateTemplate(*step.Value, vars)
		if err != nil {
			return fmt.Errorf("step %s value interpolation error: %w", step.ID, err)
		}
		resolvedValue = interpolated
	}

	if resolvedSelector != "" {
		stepResult.Selector = resolvedSelector
	}

	switch action {
	case "goto":
		if resolvedURL == "" {
			return fmt.Errorf("step %s requires url for goto action", step.ID)
		}
		targetURL, err := resolveSyntheticBrowserURL(startURL, *currentURL, resolvedURL)
		if err != nil {
			return fmt.Errorf("step %s url resolution failed: %w", step.ID, err)
		}
		stepResult.URL = targetURL
		if err := chromedp.Run(ctx,
			chromedp.Navigate(targetURL),
			chromedp.Location(currentURL),
		); err != nil {
			return fmt.Errorf("step %s navigation failed: %w", step.ID, err)
		}
		return nil

	case "click":
		if resolvedSelector == "" {
			return fmt.Errorf("step %s requires selector for click action", step.ID)
		}
		if err := chromedp.Run(ctx,
			chromedp.WaitVisible(resolvedSelector, chromedp.ByQuery),
			chromedp.Click(resolvedSelector, chromedp.ByQuery),
			chromedp.Location(currentURL),
		); err != nil {
			return newSyntheticBrowserStepFailure("step %s click failed: %v", step.ID, err)
		}
		return nil

	case "fill":
		if resolvedSelector == "" {
			return fmt.Errorf("step %s requires selector for fill action", step.ID)
		}
		if strings.TrimSpace(resolvedValue) == "" {
			return fmt.Errorf("step %s requires value for fill action", step.ID)
		}
		// Clear via JS instead of chromedp.SetValue(sel, ""): SetValue's
		// round-trip verification rejects empty strings on Chrome >= 149.
		clearScript := fmt.Sprintf(`(() => { const el = document.querySelector(%q); if (el) { el.value = ""; } })()`, resolvedSelector)
		if err := chromedp.Run(ctx,
			chromedp.WaitVisible(resolvedSelector, chromedp.ByQuery),
			chromedp.Evaluate(clearScript, nil),
			chromedp.SendKeys(resolvedSelector, resolvedValue, chromedp.ByQuery),
		); err != nil {
			return newSyntheticBrowserStepFailure("step %s fill failed: %v", step.ID, err)
		}
		return nil

	case "wait_for":
		if resolvedSelector == "" {
			return fmt.Errorf("step %s requires selector for wait_for action", step.ID)
		}
		if err := chromedp.Run(ctx,
			chromedp.WaitVisible(resolvedSelector, chromedp.ByQuery),
		); err != nil {
			return newSyntheticBrowserStepFailure("step %s wait_for failed: %v", step.ID, err)
		}
		return nil

	case "assert_visible":
		if resolvedSelector == "" {
			return fmt.Errorf("step %s requires selector for assert_visible action", step.ID)
		}
		if err := chromedp.Run(ctx,
			chromedp.WaitVisible(resolvedSelector, chromedp.ByQuery),
		); err != nil {
			return newSyntheticBrowserStepFailure("step %s assert_visible failed: %v", step.ID, err)
		}
		return nil

	case "assert_text":
		if resolvedSelector == "" {
			return fmt.Errorf("step %s requires selector for assert_text action", step.ID)
		}
		if strings.TrimSpace(resolvedValue) == "" {
			return fmt.Errorf("step %s requires value for assert_text action", step.ID)
		}
		var actualText string
		if err := chromedp.Run(ctx,
			chromedp.WaitVisible(resolvedSelector, chromedp.ByQuery),
			chromedp.Text(resolvedSelector, &actualText, chromedp.ByQuery, chromedp.NodeVisible),
		); err != nil {
			return newSyntheticBrowserStepFailure("step %s assert_text failed: %v", step.ID, err)
		}
		if !strings.Contains(actualText, resolvedValue) {
			return newSyntheticBrowserStepFailure("step %s assert_text failed: expected %q in %q", step.ID, resolvedValue, actualText)
		}
		return nil

	case "assert_url":
		if strings.TrimSpace(resolvedValue) == "" {
			return fmt.Errorf("step %s requires value for assert_url action", step.ID)
		}
		var location string
		if err := chromedp.Run(ctx, chromedp.Location(&location)); err != nil {
			return fmt.Errorf("step %s could not read current URL: %w", step.ID, err)
		}
		*currentURL = location
		stepResult.URL = location
		if !strings.Contains(location, resolvedValue) {
			return newSyntheticBrowserStepFailure("step %s assert_url failed: expected URL to contain %q, got %q", step.ID, resolvedValue, location)
		}
		return nil

	default:
		return fmt.Errorf("step %s has unsupported action %q", step.ID, step.Action)
	}
}

func resolveSyntheticBrowserURL(startURL, currentURL, raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if parsed.IsAbs() {
		return parsed.String(), nil
	}

	baseCandidate := strings.TrimSpace(currentURL)
	if baseCandidate == "" {
		baseCandidate = strings.TrimSpace(startURL)
	}
	baseURL, err := url.Parse(baseCandidate)
	if err != nil {
		return "", fmt.Errorf("invalid base URL %q: %w", baseCandidate, err)
	}
	return baseURL.ResolveReference(parsed).String(), nil
}

func findBrowserExecutable() string {
	envPath := strings.TrimSpace(os.Getenv("CHROME_BIN"))
	if envPath != "" {
		return envPath
	}

	candidates := []string{"chromium-browser", "chromium", "google-chrome", "google-chrome-stable"}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

func captureSyntheticBrowserScreenshot(ctx context.Context, artifactsBaseDir string) (string, error) {
	var screenshot []byte
	if err := chromedp.Run(ctx, chromedp.FullScreenshot(&screenshot, 85)); err != nil {
		return "", err
	}

	baseDir := strings.TrimSpace(artifactsBaseDir)
	if baseDir == "" {
		baseDir = filepath.Join(os.TempDir(), "probara", "synthetic-browser-artifacts")
	}

	relativePathPrefix := ""
	targetDir := baseDir
	if monitorID, ok := syntheticBrowserMonitorIDFromContext(ctx); ok {
		relativePathPrefix = monitorID
		targetDir = filepath.Join(baseDir, monitorID)
	}

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", err
	}

	filename := fmt.Sprintf("screenshot-%d.png", time.Now().UnixNano())
	fullPath := filepath.Join(targetDir, filename)
	if err := os.WriteFile(fullPath, screenshot, 0o644); err != nil {
		return "", err
	}

	relativePath := filename
	if relativePathPrefix != "" {
		relativePath = filepath.ToSlash(filepath.Join(relativePathPrefix, filename))
	}

	return relativePath, nil
}

func (c *SyntheticBrowserChecker) captureScreenshotWithFallback(browserCtx, allocCtx context.Context, currentURL string) (string, error) {
	captureCtx, cancelCapture := context.WithTimeout(browserCtx, 5*time.Second)
	defer cancelCapture()

	path, err := captureSyntheticBrowserScreenshot(captureCtx, c.artifactsDir)
	if err == nil {
		return path, nil
	}

	// If the journey context is canceled, retry in a fresh browser context so failure runs still emit screenshots.
	fallbackCtx, cancelFallback := context.WithTimeout(allocCtx, 10*time.Second)
	defer cancelFallback()

	fallbackBrowserCtx, cancelFallbackBrowser := chromedp.NewContext(fallbackCtx)
	defer cancelFallbackBrowser()

	navigateTo := strings.TrimSpace(currentURL)
	if navigateTo != "" {
		if navErr := chromedp.Run(fallbackBrowserCtx, chromedp.Navigate(navigateTo)); navErr != nil {
			return "", fmt.Errorf("%w; fallback navigate failed: %v", err, navErr)
		}
	}

	fallbackPath, fallbackErr := captureSyntheticBrowserScreenshot(fallbackBrowserCtx, c.artifactsDir)
	if fallbackErr != nil {
		return "", fmt.Errorf("%w; fallback capture failed: %v", err, fallbackErr)
	}

	return fallbackPath, nil
}
