package config

import (
	"testing"

	"github.com/yassinebenameur/probara/shared/models"
)

// The API (run-now), the scheduler and the worker must resolve the same queue
// names from the same environment, defaults included.
func TestCheckQueueNamesAgreeAcrossServices(t *testing.T) {
	base := map[string]string{
		"HTTP_PORT": "8080", "METRICS_PORT": "9090",
		"POSTGRES_URL": "postgres://u@localhost/db", "NATS_URL": "nats://localhost:4222",
		"ADMIN_JWT_SECRET": "0123456789abcdef0123456789abcdef", "LOCAL_AI_DISABLED": "true",
	}
	for name, env := range map[string]map[string]string{
		"defaults": {},
		"overrides": {
			"CHECK_JOB_STREAM": "check-jobs", "CHECK_JOB_SUBJECT": "check.job",
			"CHECK_RESULT_STREAM": "results", "CHECK_RESULT_SUBJECT": "check.result",
		},
	} {
		t.Run(name, func(t *testing.T) {
			for k, v := range base {
				t.Setenv(k, v)
			}
			for _, k := range []string{"CHECK_JOB_STREAM", "CHECK_JOB_SUBJECT", "CHECK_RESULT_STREAM", "CHECK_RESULT_SUBJECT"} {
				t.Setenv(k, env[k])
			}
			want := CheckQueueConfig{
				JobStream: models.CheckJobStream, JobSubject: models.CheckJobSubject,
				ResultStream: models.CheckResultStream, ResultSubject: models.CheckResultSubject,
			}
			if len(env) > 0 {
				want = CheckQueueConfig{JobStream: "check-jobs", JobSubject: "check.job", ResultStream: "results", ResultSubject: "check.result"}
			}

			api, err := LoadAPIConfig()
			if err != nil {
				t.Fatalf("LoadAPIConfig: %v", err)
			}
			sched, err := LoadSchedulerConfig()
			if err != nil {
				t.Fatalf("LoadSchedulerConfig: %v", err)
			}
			worker, err := LoadWorkerConfig()
			if err != nil {
				t.Fatalf("LoadWorkerConfig: %v", err)
			}

			if got := (CheckQueueConfig{api.CheckJobStream, api.CheckJobSubject, want.ResultStream, api.CheckResultSubject}); got != want {
				t.Errorf("api = %+v, want %+v", got, want)
			}
			if got := (CheckQueueConfig{sched.CheckJobStream, sched.CheckJobSubject, sched.CheckResultStream, sched.CheckResultSubject}); got != want {
				t.Errorf("scheduler = %+v, want %+v", got, want)
			}
			if got := (CheckQueueConfig{worker.CheckJobStream, worker.CheckJobSubject, worker.CheckResultStream, worker.CheckResultSubject}); got != want {
				t.Errorf("worker = %+v, want %+v", got, want)
			}
		})
	}
}
