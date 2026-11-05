package present

import (
	"fmt"

	"github.com/yassinebenameur/probara/shared/notifications"
)

// Relapse wording. An availability alert that goes down again within its
// workspace's reopen cooldown is reopened rather than replaced, and its page
// is delivered as a "created" event with Alert.RelapseCount > 0 (plugins see
// no new event type). These helpers give that page its own wording, "Down
// again (relapse n)", and add the relapse facts to every event of a relapsed
// alert. Only availability alerts reopen.

// isRelapsed reports whether alert has relapsed at least once.
func isRelapsed(alert notifications.AlertDetails) bool {
	return alert.RelapseCount > 0 && (alert.Kind == "" || alert.Kind == notifications.KindAvailability)
}

// isRelapsePage reports whether this event is the page of a relapse.
func isRelapsePage(alert notifications.AlertDetails, eventType string, resolved bool) bool {
	return isRelapsed(alert) && eventType == EventCreated && !resolved
}

// relapseLabel is the headline label of a relapse's page.
func relapseLabel(alert notifications.AlertDetails, eventType string) (string, bool) {
	if !isRelapsePage(alert, eventType, alert.Status == "resolved") {
		return "", false
	}
	return "Down Again", true
}

// relapseTone is the tone and status word of a relapse's page.
func relapseTone(alert notifications.AlertDetails, eventType string, resolved bool) (Tone, string, bool) {
	if !isRelapsePage(alert, eventType, resolved) {
		return "", "", false
	}
	return ToneDown, "DOWN AGAIN", true
}

// relapseSummary is the one-sentence summary of a relapse's page.
func relapseSummary(alert notifications.AlertDetails, eventType string, resolved bool) (string, bool) {
	if !isRelapsePage(alert, eventType, resolved) {
		return "", false
	}
	s := fmt.Sprintf("Down again (relapse %d)", alert.RelapseCount)
	if checks := checkPhrase(alert.FailureCount); checks != "" {
		return fmt.Sprintf("%s — %s failed in a row.", s, checks), true
	}
	return s + " — stopped responding shortly after it recovered.", true
}

// relapseRows are the detail rows every event of a relapsed alert carries.
func relapseRows(alert notifications.AlertDetails) []Row {
	if !isRelapsed(alert) {
		return nil
	}
	rows := []Row{{Label: "Relapses", Value: fmt.Sprintf("%d", alert.RelapseCount)}}
	if alert.ReopenedAt != nil {
		rows = append(rows, Row{Label: "Reopened", Value: HumanTime(*alert.ReopenedAt)})
	}
	return rows
}
