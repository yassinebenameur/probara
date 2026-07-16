package locationnatsauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

type fakeAuthenticator struct {
	wantID, wantCredential string
	err                    error
}

func (f fakeAuthenticator) AuthenticateWorker(_ context.Context, id, credential string) error {
	if f.err != nil {
		return f.err
	}
	if id != f.wantID || credential != f.wantCredential {
		return errors.New("invalid credentials")
	}
	return nil
}

func newTestService(t *testing.T, auth WorkerAuthenticator) *Service {
	t.Helper()
	kp, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	seed, err := kp.Seed()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(string(seed), auth, Options{CheckJobStream: "JOBS", ResultSubject: "results"})
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	return svc
}

func TestAuthorizeScopesLocationWorker(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	user, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	userPublic, _ := user.PublicKey()
	svc := newTestService(t, fakeAuthenticator{wantID: id, wantCredential: "secret"})

	token, err := svc.Authorize(&jwt.AuthorizationRequest{
		UserNkey:       userPublic,
		ConnectOptions: jwt.ConnectOptions{Username: id, Password: "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := jwt.DecodeUserClaims(token)
	if err != nil {
		t.Fatal(err)
	}

	wantConsumer := "check-workers-loc-" + id
	assertContains(t, claims.Permissions.Pub.Allow, "$JS.API.CONSUMER.MSG.NEXT.JOBS."+wantConsumer)
	assertContains(t, claims.Permissions.Pub.Allow, "$JS.ACK.JOBS."+wantConsumer+".>")
	assertContains(t, claims.Permissions.Pub.Allow, "results.loc."+id)
	for _, permission := range claims.Permissions.Pub.Allow {
		if permission == "results" {
			t.Fatal("location worker must not publish the platform result subject")
		}
	}
	assertContains(t, claims.Permissions.Sub.Allow, "checks.test.loc."+id)
	for _, permission := range append(claims.Permissions.Pub.Allow, claims.Permissions.Sub.Allow...) {
		other := "22222222-2222-4222-8222-222222222222"
		if permission == "checks.test.loc."+other || permission == "$JS.ACK.CHECK_JOBS.check-workers-loc-"+other+".>" {
			t.Fatalf("claim grants another location: %q", permission)
		}
	}
}

func TestAuthorizeRejectsInvalidCredentials(t *testing.T) {
	user, _ := nkeys.CreateUser()
	userPublic, _ := user.PublicKey()
	svc := newTestService(t, fakeAuthenticator{err: errors.New("no")})
	if _, err := svc.Authorize(&jwt.AuthorizationRequest{
		UserNkey:       userPublic,
		ConnectOptions: jwt.ConnectOptions{Username: "11111111-1111-4111-8111-111111111111", Password: "bad"},
	}); err == nil {
		t.Fatal("expected invalid credentials to be rejected")
	}
}

func TestNewRejectsNonAccountSeed(t *testing.T) {
	user, _ := nkeys.CreateUser()
	seed, _ := user.Seed()
	if _, err := New(string(seed), fakeAuthenticator{}, Options{CheckJobStream: "JOBS", ResultSubject: "results"}); err == nil {
		t.Fatal("expected user seed to be rejected")
	}
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("%q not found in %v", want, values)
}
