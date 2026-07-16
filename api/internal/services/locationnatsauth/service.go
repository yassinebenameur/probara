// Package locationnatsauth provides NATS authorization-callout credentials for
// private-location workers. The NATS server enforces the returned permissions,
// so a worker cannot consume or acknowledge another location's jobs.
package locationnatsauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	callout "github.com/synadia-io/callout.go"

	"github.com/yassinebenameur/probara/shared/models"
)

const userJWTLifetime = time.Hour

// WorkerAuthenticator validates an active location's worker credential.
type WorkerAuthenticator interface {
	AuthenticateWorker(ctx context.Context, locationID, credential string) error
}

// Service issues short-lived, location-scoped NATS user claims.
type Service struct {
	authenticator  WorkerAuthenticator
	issuer         nkeys.KeyPair
	checkJobStream string
	resultSubject  string
	now            func() time.Time
}

// Options contains deployment-specific stream and subject names.
type Options struct {
	CheckJobStream string
	ResultSubject  string
}

// New validates an account seed and constructs the authorizer.
func New(issuerSeed string, authenticator WorkerAuthenticator, opts Options) (*Service, error) {
	if authenticator == nil {
		return nil, errors.New("location worker authenticator is required")
	}
	issuer, err := nkeys.FromSeed([]byte(strings.TrimSpace(issuerSeed)))
	if err != nil {
		return nil, fmt.Errorf("parse NATS location auth issuer seed: %w", err)
	}
	public, err := issuer.PublicKey()
	if err != nil || !nkeys.IsValidPublicAccountKey(public) {
		return nil, errors.New("NATS location auth issuer must be an account seed")
	}
	if strings.TrimSpace(opts.CheckJobStream) == "" || strings.TrimSpace(opts.ResultSubject) == "" {
		return nil, errors.New("NATS location auth stream and result subject are required")
	}
	return &Service{
		authenticator:  authenticator,
		issuer:         issuer,
		checkJobStream: opts.CheckJobStream,
		resultSubject:  opts.ResultSubject,
		now:            time.Now,
	}, nil
}

// Authorize validates username/password credentials and returns a user JWT
// whose publish and subscribe permissions are restricted to that location.
func (s *Service) Authorize(req *jwt.AuthorizationRequest) (string, error) {
	if req == nil {
		return "", errors.New("authorization request is required")
	}
	locationID, err := uuid.Parse(strings.TrimSpace(req.ConnectOptions.Username))
	if err != nil || strings.TrimSpace(req.ConnectOptions.Password) == "" {
		return "", callout.ErrRejectedAuth
	}
	if err := s.authenticator.AuthenticateWorker(context.Background(), locationID.String(), req.ConnectOptions.Password); err != nil {
		return "", callout.ErrRejectedAuth
	}

	id := locationID.String()
	consumer := models.CheckJobConsumerForLocation(id)
	claims := jwt.NewUserClaims(req.UserNkey)
	if claims == nil {
		return "", errors.New("authorization request has no user nkey")
	}
	claims.Name = "location-" + id
	claims.Audience = "$G"
	claims.Expires = s.now().Add(userJWTLifetime).Unix()
	claims.Permissions.Pub.Allow.Add(
		models.CheckResultSubjectForLocation(s.resultSubject, id),
		"locations.heartbeat",
		"$JS.API.CONSUMER.INFO."+s.checkJobStream+"."+consumer,
		"$JS.API.CONSUMER.MSG.NEXT."+s.checkJobStream+"."+consumer,
		"$JS.ACK."+s.checkJobStream+"."+consumer+".>",
	)
	claims.Permissions.Sub.Allow.Add(
		"_INBOX.>",
		"checks.test.loc."+id,
	)
	claims.Permissions.Resp = &jwt.ResponsePermission{MaxMsgs: 1, Expires: time.Minute}
	return claims.Encode(s.issuer)
}

// Start registers the NATS authorization-callout service on an already
// authenticated platform connection.
func (s *Service) Start(nc *nats.Conn) (*callout.AuthorizationService, error) {
	if nc == nil {
		return nil, errors.New("NATS connection is required")
	}
	return callout.NewAuthorizationService(
		nc,
		callout.Authorizer(s.Authorize),
		callout.ResponseSignerKey(s.issuer),
	)
}
