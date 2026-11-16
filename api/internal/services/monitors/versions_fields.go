package monitors

import "github.com/yassinebenameur/probara/api/internal/models"

// snapshotColumns are the monitors columns a configuration version records
// and a restore writes back one to one: loadSnapshot reads them and
// RestoreRequest sends each that differs from the live monitor.
//
// A new top-level monitor setting that PATCH /monitors/{id} accepts is one
// entry here plus its field on models.MonitorConfigSnapshot. Settings stored
// inside `config` need nothing: the whole config is versioned. Only settings
// that interact with another one (type, config, tags, notification mode and
// channels, locations and quorum, dependencies, group members) are handled
// by hand in loadSnapshot and RestoreRequest.
var snapshotColumns = []snapshotColumn{
	column("name", func(s *models.MonitorConfigSnapshot) *string { return &s.Name },
		func(r *models.UpdateMonitorRequest, v *string) { r.Name = v }),
	column("interval_seconds", func(s *models.MonitorConfigSnapshot) *int { return &s.IntervalSeconds },
		func(r *models.UpdateMonitorRequest, v *int) { r.IntervalSeconds = v }),
	column("timeout_seconds", func(s *models.MonitorConfigSnapshot) *int { return &s.TimeoutSeconds },
		func(r *models.UpdateMonitorRequest, v *int) { r.TimeoutSeconds = v }),
	column("consecutive_failures_threshold", func(s *models.MonitorConfigSnapshot) *int { return &s.ConsecutiveFailuresThreshold },
		func(r *models.UpdateMonitorRequest, v *int) { r.ConsecutiveFailuresThreshold = v }),
	column("member_alert_rollup", func(s *models.MonitorConfigSnapshot) *string { return &s.MemberAlertRollup },
		func(r *models.UpdateMonitorRequest, v *string) { r.MemberAlertRollup = v }),
	column("dependency_suppression", func(s *models.MonitorConfigSnapshot) *string { return &s.DependencySuppression },
		func(r *models.UpdateMonitorRequest, v *string) { r.DependencySuppression = v }),
}

type snapshotColumn struct {
	name string
	// dest is the Scan destination for the column.
	dest func(*models.MonitorConfigSnapshot) any
	// restore sets the request field when version and current differ.
	restore func(version, current *models.MonitorConfigSnapshot, req *models.UpdateMonitorRequest) bool
}

func column[T comparable](name string, field func(*models.MonitorConfigSnapshot) *T, set func(*models.UpdateMonitorRequest, *T)) snapshotColumn {
	return snapshotColumn{
		name: name,
		dest: func(s *models.MonitorConfigSnapshot) any { return field(s) },
		restore: func(version, current *models.MonitorConfigSnapshot, req *models.UpdateMonitorRequest) bool {
			if *field(version) == *field(current) {
				return false
			}
			value := *field(version)
			set(req, &value)
			return true
		},
	}
}
