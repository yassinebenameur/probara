package locationnatsauth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	server "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/yassinebenameur/probara/shared/models"
)

type credentialMap map[string]string

func (m credentialMap) AuthenticateWorker(_ context.Context, id, credential string) error {
	if m[id] != credential {
		return fmt.Errorf("invalid credentials")
	}
	return nil
}

func TestAuthorizationCalloutEnforcesLocationConsumerBoundary(t *testing.T) {
	account, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := account.Seed()
	public, _ := account.PublicKey()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "nats.conf")
	config := fmt.Sprintf(`
port: -1
jetstream { store_dir: %q }
authorization {
  users: [{ user: "platform", password: "platform-secret" }]
  auth_callout {
    issuer: %q
    auth_users: ["platform"]
  }
}
`, filepath.Join(dir, "js"), public)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := server.ProcessConfigFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server did not start")
	}
	t.Cleanup(srv.Shutdown)

	platform, err := nats.Connect(srv.ClientURL(), nats.UserInfo("platform", "platform-secret"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(platform.Close)

	const locationOne = "11111111-1111-4111-8111-111111111111"
	const locationTwo = "22222222-2222-4222-8222-222222222222"
	authorizer, err := New(string(seed), credentialMap{locationOne: "one", locationTwo: "two"}, Options{
		CheckJobStream: "CHECK_JOBS",
		ResultSubject:  "check.results",
	})
	if err != nil {
		t.Fatal(err)
	}
	calloutService, err := authorizer.Start(platform)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = calloutService.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	platformJS, err := jetstream.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := platformJS.CreateStream(ctx, jetstream.StreamConfig{
		Name: "CHECK_JOBS", Subjects: []string{"check.jobs.>"}, Retention: jetstream.WorkQueuePolicy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := platformJS.CreateStream(ctx, jetstream.StreamConfig{
		Name: "CHECK_RESULTS", Subjects: []string{"check.results", "check.results.loc.>"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{locationOne, locationTwo} {
		if _, err := stream.CreateConsumer(ctx, jetstream.ConsumerConfig{
			Name: models.CheckJobConsumerForLocation(id), Durable: models.CheckJobConsumerForLocation(id),
			FilterSubject: "check.jobs.loc." + id, AckPolicy: jetstream.AckExplicitPolicy,
		}); err != nil {
			t.Fatal(err)
		}
	}

	worker, err := nats.Connect(srv.ClientURL(), nats.UserInfo(locationOne, "one"), nats.Timeout(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Close)
	workerJS, err := jetstream.New(worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workerJS.Publish(ctx, "check.results.loc."+locationOne, []byte("result")); err != nil {
		t.Fatalf("publish own scoped result: %v", err)
	}
	shortPublishCtx, shortPublishCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer shortPublishCancel()
	if _, err := workerJS.Publish(shortPublishCtx, "check.results", []byte("forged platform result")); err == nil {
		t.Fatal("location worker published on platform result subject")
	}
	ownConsumer, err := workerJS.Consumer(ctx, "CHECK_JOBS", models.CheckJobConsumerForLocation(locationOne))
	if err != nil {
		t.Fatalf("own consumer must be accessible: %v", err)
	}
	if _, err := platformJS.Publish(ctx, "check.jobs.loc."+locationOne, []byte("job")); err != nil {
		t.Fatal(err)
	}
	messages, err := ownConsumer.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("fetch own job: %v", err)
	}
	message := <-messages.Messages()
	if message == nil {
		t.Fatal("own location job was not delivered")
	}
	if err := message.Ack(); err != nil {
		t.Fatalf("ack own job: %v", err)
	}
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer shortCancel()
	if _, err := workerJS.Consumer(shortCtx, "CHECK_JOBS", models.CheckJobConsumerForLocation(locationTwo)); err == nil {
		t.Fatal("worker accessed another location's durable consumer")
	}
}
