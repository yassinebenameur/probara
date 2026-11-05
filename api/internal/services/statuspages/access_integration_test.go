//go:build !unit

package statuspages

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/pageaccess"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestService_PageVisibilityAndPassword(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	svc := NewService(dbClient, nil)

	// Default is public, without a password.
	page, err := svc.CreateStatusPage(ctx, tenantID, &models.CreateStatusPageRequest{Slug: "acc-default", Title: "Default"})
	require.NoError(t, err)
	require.Equal(t, pageaccess.Public, page.Visibility)
	require.False(t, page.HasPassword)

	// Password visibility needs a password.
	_, err = svc.CreateStatusPage(ctx, tenantID, &models.CreateStatusPageRequest{
		Slug: "acc-nopw", Title: "No password", Visibility: strPtr(pageaccess.Password),
	})
	var verr *ValidationError
	require.True(t, errors.As(err, &verr), "got %v", err)

	page, err = svc.CreateStatusPage(ctx, tenantID, &models.CreateStatusPageRequest{
		Slug: "acc-pw", Title: "Password", Visibility: strPtr(pageaccess.Password), Password: strPtr("open sesame"),
	})
	require.NoError(t, err)
	require.Equal(t, pageaccess.Password, page.Visibility)
	require.True(t, page.HasPassword)

	stored := func() (hash string, version int) {
		var h *string
		require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT password_hash, password_version FROM status_pages WHERE id = $1`, page.ID).Scan(&h, &version))
		if h != nil {
			hash = *h
		}
		return hash, version
	}
	hash, version := stored()
	require.True(t, pageaccess.CheckPassword(hash, "open sesame"))
	require.Equal(t, 1, version)

	// Absent keeps; other fields do not touch it.
	_, err = svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{Title: strPtr("Renamed")})
	require.NoError(t, err)
	hash2, version2 := stored()
	require.Equal(t, hash, hash2)
	require.Equal(t, 1, version2)

	// Replace bumps the version.
	_, err = svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{Password: strPtr("new password")})
	require.NoError(t, err)
	hash, version = stored()
	require.True(t, pageaccess.CheckPassword(hash, "new password"))
	require.Equal(t, 2, version)

	// Clearing the password of a password page is refused.
	_, err = svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{Password: strPtr("")})
	require.True(t, errors.As(err, &verr), "got %v", err)

	// Moving to private and clearing works, and bumps again.
	updated, err := svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{
		Visibility: strPtr(pageaccess.Private), Password: strPtr(""),
	})
	require.NoError(t, err)
	require.Equal(t, pageaccess.Private, updated.Visibility)
	require.False(t, updated.HasPassword)
	hash, version = stored()
	require.Empty(t, hash)
	require.Equal(t, 3, version)

	// Back to password without a password is refused.
	_, err = svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{Visibility: strPtr(pageaccess.Password)})
	require.True(t, errors.As(err, &verr), "got %v", err)

	// Another tenant cannot touch it.
	otherTenant := testutil.InsertTenant(ctx, t, dbClient, "")
	_, err = svc.UpdateStatusPage(ctx, otherTenant, page.ID, &models.UpdateStatusPageRequest{Visibility: strPtr(pageaccess.Public)})
	require.EqualError(t, err, "status page not found")

	list, err := svc.ListStatusPages(ctx, tenantID, 1, 20)
	require.NoError(t, err)
	byID := map[uuid.UUID]models.StatusPage{}
	for _, p := range list.Items {
		byID[p.ID] = p
	}
	require.Equal(t, pageaccess.Private, byID[page.ID].Visibility)
}

func TestService_AccessTokens(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	otherTenant := testutil.InsertTenant(ctx, t, dbClient, "")
	svc := NewService(dbClient, nil)

	page, err := svc.CreateStatusPage(ctx, tenantID, &models.CreateStatusPageRequest{
		Slug: "acc-tokens", Title: "Tokens", Visibility: strPtr(pageaccess.Private),
	})
	require.NoError(t, err)

	created, err := svc.CreateAccessToken(ctx, tenantID, page.ID, nil, "  Lobby TV  ")
	require.NoError(t, err)
	require.Equal(t, "Lobby TV", created.Label)
	require.Contains(t, created.Token, pageaccess.TokenPrefix)

	// Only the hash is stored.
	var storedHash string
	require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT token_hash FROM status_page_access_tokens WHERE id = $1`, created.ID).Scan(&storedHash))
	require.Equal(t, pageaccess.HashToken(created.Token), storedHash)

	list, err := svc.ListAccessTokens(ctx, tenantID, page.ID)
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	require.Nil(t, list.Items[0].RevokedAt)

	// Tenant isolation.
	_, err = svc.ListAccessTokens(ctx, otherTenant, page.ID)
	require.EqualError(t, err, "status page not found")
	_, err = svc.CreateAccessToken(ctx, otherTenant, page.ID, nil, "x")
	require.EqualError(t, err, "status page not found")
	require.ErrorIs(t, svc.RevokeAccessToken(ctx, otherTenant, page.ID, created.ID), ErrAccessTokenNotFound)

	require.NoError(t, svc.RevokeAccessToken(ctx, tenantID, page.ID, created.ID))
	require.NoError(t, svc.RevokeAccessToken(ctx, tenantID, page.ID, created.ID), "idempotent")
	require.ErrorIs(t, svc.RevokeAccessToken(ctx, tenantID, page.ID, uuid.New()), ErrAccessTokenNotFound)
	list, err = svc.ListAccessTokens(ctx, tenantID, page.ID)
	require.NoError(t, err)
	require.NotNil(t, list.Items[0].RevokedAt)

	// The active-token cap.
	for i := 0; i < maxActiveAccessTokens; i++ {
		_, err := svc.CreateAccessToken(ctx, tenantID, page.ID, nil, "kiosk")
		require.NoError(t, err)
	}
	_, err = svc.CreateAccessToken(ctx, tenantID, page.ID, nil, "one too many")
	var verr *ValidationError
	require.True(t, errors.As(err, &verr), "got %v", err)

	// Deleting the page removes its tokens.
	require.NoError(t, svc.DeleteStatusPage(ctx, tenantID, page.ID))
	var n int
	require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM status_page_access_tokens WHERE status_page_id = $1`, page.ID).Scan(&n))
	require.Zero(t, n)
}
