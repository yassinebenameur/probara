"use client";

import { Suspense, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import Button from "@/components/ui/Button";
import BrandMark from "@/components/ui/BrandMark";
import Callout from "@/components/ui/Callout";
import { clearApiKey } from "@/lib/auth";
import { getAuthCapabilities, getOidcStatus, loginWithMFA, requestPasswordReset } from "@/lib/api";
import type { OidcStatus } from "@/lib/types";

type BootstrapStatusResponse = {
  has_admin_users: boolean;
};

type ApiErrorResponse = {
  message?: string;
};

// "mfa": the password was right and the account has two-factor on; "forgot"
// and "forgot-sent": the password-reset request (only when the server can
// mail, per /auth/capabilities).
type AuthMode = "loading" | "login" | "bootstrap" | "mfa" | "forgot" | "forgot-sent";

const SSO_ERROR_MESSAGES: Record<string, string> = {
  not_provisioned:
    "No account is provisioned for this identity. Ask an administrator to invite you.",
  state_mismatch: "The sign-in attempt expired or was tampered with. Please try again.",
  exchange_failed: "Single sign-on failed while talking to the identity provider.",
  provider_denied: "The identity provider rejected the sign-in.",
  provider_unreachable: "The identity provider is unreachable. Try again later.",
  internal: "Single sign-on failed unexpectedly. Please try again.",
};

// Where to go after signing in: a same-origin path only (mirrors the API's
// sanitizeNextPath for SSO), so a crafted link cannot bounce elsewhere. The
// MCP consent screen sends people here with next=/oauth/consent?….
function safeNextPath(next: string | null): string {
  if (!next || !next.startsWith("/") || next.startsWith("//") || /[\\\r\n]/.test(next)) {
    return "/";
  }
  return next;
}

export default function LoginPage() {
  return (
    <Suspense>
      <LoginPageInner />
    </Suspense>
  );
}

function LoginPageInner() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const [authMode, setAuthMode] = useState<AuthMode>("loading");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [oidc, setOidc] = useState<OidcStatus>({ enabled: false });
  const [canReset, setCanReset] = useState(false);
  const [mfaToken, setMfaToken] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [forgotLogin, setForgotLogin] = useState("");
  const nextPath = safeNextPath(searchParams.get("next"));

  useEffect(() => {
    const ssoError = searchParams.get("sso_error");
    if (ssoError) {
      setError(SSO_ERROR_MESSAGES[ssoError] || SSO_ERROR_MESSAGES.internal);
    }
  }, [searchParams]);

  useEffect(() => {
    let ignore = false;
    getOidcStatus().then((status) => {
      if (!ignore) setOidc(status);
    });
    getAuthCapabilities().then((caps) => {
      if (!ignore) setCanReset(caps.password_reset);
    });
    return () => {
      ignore = true;
    };
  }, []);

  useEffect(() => {
    let ignore = false;

    const loadBootstrapStatus = async () => {
      try {
        const response = await fetch("/api/v1/users/bootstrap/status", {
          method: "GET",
          credentials: "include",
        });

        if (!response.ok) {
          throw new Error("status lookup failed");
        }

        const data = (await response.json()) as BootstrapStatusResponse;
        if (!ignore) {
          setAuthMode(data.has_admin_users ? "login" : "bootstrap");
        }
      } catch {
        if (!ignore) {
          setAuthMode("login");
          setError(
            "Unable to check initial admin status. You can still sign in.",
          );
        }
      }
    };

    loadBootstrapStatus();

    return () => {
      ignore = true;
    };
  }, []);

  const readErrorMessage = async (response: Response, fallback: string) => {
    try {
      const data = (await response.json()) as ApiErrorResponse;
      return data.message || fallback;
    } catch {
      return fallback;
    }
  };

  const loginWithCredentials = async (
    user: string,
    pass: string,
  ): Promise<string | null> => {
    try {
      const response = await fetch("/api/v1/auth/login", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        credentials: "include",
        body: JSON.stringify({
          username: user,
          password: pass,
        }),
      });

      if (response.status === 401) {
        return "Invalid username or password";
      }

      // One answer for a locked account and a locked unknown username alike
      // (the API never says which).
      if (response.status === 429) {
        return "Too many failed sign-in attempts. Try again later.";
      }

      if (!response.ok) {
        return await readErrorMessage(response, "Login failed");
      }

      // Two-factor on: no session yet, ask for the code.
      const data = (await response.json().catch(() => ({}))) as { mfa_required?: boolean; mfa_token?: string };
      if (data.mfa_required && data.mfa_token) {
        setMfaToken(data.mfa_token);
        setMfaCode("");
        setPassword("");
        setAuthMode("mfa");
        return null;
      }

      // Ensure we don't fall back to API-key mode after admin login
      clearApiKey();

      router.push(nextPath);
      return null;
    } catch {
      return "Failed to sign in. Please try again.";
    }
  };

  const handleLoginSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setLoading(true);

    if (!username.trim() || !password.trim()) {
      setError("Username and password are required");
      setLoading(false);
      return;
    }

    const loginError = await loginWithCredentials(username.trim(), password);
    if (loginError) {
      setError(loginError);
    }
    setLoading(false);
  };

  const handleMfaSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!mfaCode.trim()) {
      setError("Enter the code from your authenticator app");
      return;
    }
    setError("");
    setLoading(true);
    try {
      await loginWithMFA(mfaToken, mfaCode.trim());
      clearApiKey();
      router.push(nextPath);
    } catch (err: any) {
      if (err?.error === "mfa_token_invalid") {
        setAuthMode("login");
        setError("The sign-in attempt expired. Enter your password again.");
      } else if (err?.status === 429) {
        setError("Too many failed sign-in attempts. Try again later.");
      } else {
        setError("Invalid code");
      }
      setLoading(false);
    }
  };

  const handleForgotSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!forgotLogin.trim()) {
      setError("Enter your username or email address");
      return;
    }
    setError("");
    setLoading(true);
    try {
      await requestPasswordReset(forgotLogin.trim());
      setAuthMode("forgot-sent");
    } catch (err: any) {
      setError(err?.status === 429 ? "Too many requests. Try again in a minute." : err?.message || "Request failed");
    } finally {
      setLoading(false);
    }
  };

  const backToLogin = () => {
    setError("");
    setMfaToken("");
    setAuthMode("login");
  };

  const handleBootstrapSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setLoading(true);

    if (!username.trim() || !password.trim()) {
      setError("Username and password are required");
      setLoading(false);
      return;
    }
    if (password !== confirmPassword) {
      setError("Passwords do not match");
      setLoading(false);
      return;
    }

    try {
      const response = await fetch("/api/v1/users/bootstrap/first", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        credentials: "include",
        body: JSON.stringify({
          username: username.trim(),
          password,
        }),
      });

      if (response.status === 409) {
        setAuthMode("login");
        setError("An admin user already exists. Please sign in.");
        setLoading(false);
        return;
      }

      if (!response.ok) {
        setError(
          await readErrorMessage(response, "Failed to create first admin user"),
        );
        setLoading(false);
        return;
      }

      const loginError = await loginWithCredentials(username.trim(), password);
      if (loginError) {
        setAuthMode("login");
        setError("Admin user created. Please sign in.");
        setLoading(false);
      }
    } catch {
      setError("Failed to create first admin user. Please try again.");
      setLoading(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center px-4 py-10">
      <div className="glass-overlay w-full max-w-md p-8">
        <div className="mb-6 flex justify-center">
          <BrandMark size={48} />
        </div>

        <div className="mb-8 text-center">
          {authMode === "mfa" ? (
            <>
              <h1 className="text-2xl font-semibold tracking-tight text-white">
                Two-factor authentication
              </h1>
              <p className="mt-2 text-sm text-slate-400">
                Enter the 6-digit code from your authenticator app, or a recovery code
              </p>
            </>
          ) : authMode === "forgot" || authMode === "forgot-sent" ? (
            <>
              <h1 className="text-2xl font-semibold tracking-tight text-white">
                Reset your password
              </h1>
              <p className="mt-2 text-sm text-slate-400">
                We email a reset link to the address on your account
              </p>
            </>
          ) : authMode === "bootstrap" ? (
            <>
              <h1 className="text-2xl font-semibold tracking-tight text-white">
                Create first admin
              </h1>
              <p className="mt-2 text-sm text-slate-400">
                No admin user found. Set up the initial administrator.
              </p>
            </>
          ) : (
            <>
              <h1 className="text-2xl font-semibold tracking-tight text-white">
                Admin sign in
              </h1>
              <p className="mt-2 text-sm text-slate-400">
                Use your admin credentials to access all tenants
              </p>
            </>
          )}
        </div>

        {authMode === "loading" ? (
          <div className="py-8 text-center text-sm text-slate-400">
            Checking admin setup...
          </div>
        ) : authMode === "mfa" ? (
          <form noValidate onSubmit={handleMfaSubmit} className="space-y-5">
            <div>
              <label htmlFor="mfa-code" className="mb-1.5 block text-ui font-medium text-slate-300">
                Authentication code
              </label>
              <input
                id="mfa-code"
                name="mfa-code"
                type="text"
                inputMode="text"
                autoComplete="one-time-code"
                autoFocus
                className="input"
                placeholder="123456"
                value={mfaCode}
                onChange={(e) => setMfaCode(e.target.value)}
                disabled={loading}
              />
            </div>
            {error && <Callout tone="danger">{error}</Callout>}
            <Button type="submit" variant="accent" size="sm" loading={loading} disabled={loading} className="!h-10 w-full justify-center !text-sm">
              {loading ? "Verifying…" : "Verify and continue"}
            </Button>
            <button type="button" onClick={backToLogin} className="block w-full text-center text-xs text-slate-400 hover:text-slate-200">
              Back to sign in
            </button>
          </form>
        ) : authMode === "forgot-sent" ? (
          <div className="space-y-5">
            <Callout tone="success">
              If an account with an email address matches, a reset link is on its way. It expires in one hour.
            </Callout>
            <button type="button" onClick={backToLogin} className="block w-full text-center text-xs text-slate-400 hover:text-slate-200">
              Back to sign in
            </button>
          </div>
        ) : authMode === "forgot" ? (
          <form noValidate onSubmit={handleForgotSubmit} className="space-y-5">
            <div>
              <label htmlFor="forgot-login" className="mb-1.5 block text-ui font-medium text-slate-300">
                Username or email
              </label>
              <input
                id="forgot-login"
                name="forgot-login"
                type="text"
                autoComplete="username"
                autoFocus
                className="input"
                value={forgotLogin}
                onChange={(e) => setForgotLogin(e.target.value)}
                disabled={loading}
              />
            </div>
            {error && <Callout tone="danger">{error}</Callout>}
            <Button type="submit" variant="accent" size="sm" loading={loading} disabled={loading} className="!h-10 w-full justify-center !text-sm">
              Send reset link
            </Button>
            <button type="button" onClick={backToLogin} className="block w-full text-center text-xs text-slate-400 hover:text-slate-200">
              Back to sign in
            </button>
          </form>
        ) : (
          <form
            onSubmit={
              authMode === "bootstrap"
                ? handleBootstrapSubmit
                : handleLoginSubmit
            }
            className="space-y-5"
          >
            <div>
              <label
                htmlFor="username"
                className="mb-1.5 block text-ui font-medium text-slate-300"
              >
                Username
              </label>
              <input
                id="username"
                name="username"
                type="text"
                required
                autoComplete="username"
                className="input"
                placeholder="Enter your username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                disabled={loading}
              />
            </div>

            <div>
              <label
                htmlFor="password"
                className="mb-1.5 block text-ui font-medium text-slate-300"
              >
                Password
              </label>
              <input
                id="password"
                name="password"
                type="password"
                required
                autoComplete="current-password"
                className="input"
                placeholder="Enter your password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={loading}
              />
            </div>

            {authMode === "login" && canReset && (
              <div className="-mt-3 text-right">
                <button
                  type="button"
                  className="text-xs text-slate-400 hover:text-slate-200"
                  onClick={() => {
                    setError("");
                    setForgotLogin(username.trim());
                    setAuthMode("forgot");
                  }}
                >
                  Forgot password?
                </button>
              </div>
            )}

            {authMode === "bootstrap" && (
              <div>
                <label
                  htmlFor="confirm-password"
                  className="mb-1.5 block text-ui font-medium text-slate-300"
                >
                  Confirm password
                </label>
                <input
                  id="confirm-password"
                  name="confirm-password"
                  type="password"
                  required
                  autoComplete="new-password"
                  className="input"
                  placeholder="Confirm your password"
                  value={confirmPassword}
                  onChange={(e) => setConfirmPassword(e.target.value)}
                  disabled={loading}
                />
              </div>
            )}

            {error && <Callout tone="danger">{error}</Callout>}

            <Button
              type="submit"
              variant="accent"
              size="sm"
              loading={loading}
              disabled={loading}
              className="!h-10 w-full justify-center !text-sm"
            >
              {loading
                ? authMode === "bootstrap"
                  ? "Creating admin…"
                  : "Signing in…"
                : authMode === "bootstrap"
                  ? "Create admin and continue"
                  : "Continue to dashboard"}
            </Button>

            {oidc.enabled && authMode === "login" && (
              <>
                <div className="flex items-center gap-3">
                  <div className="h-px flex-1 bg-divider" />
                  <span className="text-xs text-slate-500">or</span>
                  <div className="h-px flex-1 bg-divider" />
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={loading}
                  className="!h-10 w-full justify-center !text-sm"
                  onClick={() => {
                    clearApiKey();
                    // A full-page navigation: the API answers with a redirect to the identity provider.
                    window.location.href =
                      nextPath === "/"
                        ? "/api/v1/auth/oidc/start"
                        : `/api/v1/auth/oidc/start?next=${encodeURIComponent(nextPath)}`;
                  }}
                >
                  Continue with {oidc.label || "SSO"}
                </Button>
              </>
            )}
          </form>
        )}
      </div>
    </div>
  );
}
