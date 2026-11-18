"use client";

import { Suspense, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import Button from "@/components/ui/Button";
import BrandMark from "@/components/ui/BrandMark";
import Callout from "@/components/ui/Callout";
import { clearApiKey } from "@/lib/auth";
import { getOidcStatus } from "@/lib/api";
import {
  bootstrapRequestBody,
  bootstrapView,
  ssoButtonVisible,
  type BootstrapStatus,
} from "@/lib/bootstrap";
import type { MfaChallengeResponse, OidcStatus } from "@/lib/types";

type ApiErrorResponse = {
  error?: string;
  message?: string;
};

// The lockout rule, stated alike for every refusal in either step: the API
// never says whether an account exists or is locked.
const LOCKOUT_NOTE =
  "Ten failed attempts in a row lock the account for 15 minutes; an administrator can unlock it sooner.";

type AuthMode = "loading" | "login" | "bootstrap";

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
  const [setupCode, setSetupCode] = useState("");
  const [setupCodeRequired, setSetupCodeRequired] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [oidc, setOidc] = useState<OidcStatus>({ enabled: false });
  // The second step of a two-factor sign-in: the password was right, the
  // session starts once a code is accepted.
  const [challenge, setChallenge] = useState<string | null>(null);
  const [code, setCode] = useState("");
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

        const view = bootstrapView((await response.json()) as BootstrapStatus);
        if (!ignore) {
          setAuthMode(view.mode);
          setSetupCodeRequired(view.setupCodeRequired);
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
        // The API answers every refusal alike (unknown user, wrong password,
        // locked account), so the lockout rule is stated for all of them.
        return `Invalid username or password. ${LOCKOUT_NOTE}`;
      }

      if (!response.ok) {
        return await readErrorMessage(response, "Login failed");
      }

      const data = (await response.json().catch(() => null)) as Partial<MfaChallengeResponse> | null;
      if (data?.mfa_required && data.mfa_token) {
        // Two-factor account: no session yet, ask for the code.
        setChallenge(data.mfa_token);
        setCode("");
        setPassword("");
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

  const handleCodeSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!challenge || !code.trim()) {
      setError("Enter the code from your authenticator app, or a recovery code.");
      return;
    }
    setError("");
    setLoading(true);
    try {
      const response = await fetch("/api/v1/auth/login/mfa", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ mfa_token: challenge, code: code.trim() }),
      });
      if (response.ok) {
        clearApiKey();
        router.push(nextPath);
        return;
      }
      const body = (await response.json().catch(() => ({}))) as ApiErrorResponse;
      if (response.status === 401 && body.error === "mfa_challenge_invalid") {
        // Expired (5 minutes), already used, or retired by a password change.
        setChallenge(null);
        setError("The sign-in attempt expired. Enter your password again.");
      } else if (response.status === 401) {
        setError(`Invalid code. ${LOCKOUT_NOTE}`);
      } else {
        setError(body.message || "Sign-in failed. Try again.");
      }
    } catch {
      setError("Failed to sign in. Please try again.");
    }
    setLoading(false);
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
    if (setupCodeRequired && !setupCode.trim()) {
      setError("The setup code is required");
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
        body: JSON.stringify(
          bootstrapRequestBody(username, password, setupCode, setupCodeRequired),
        ),
      });

      if (response.status === 403) {
        setError("The setup code is not valid.");
        setLoading(false);
        return;
      }

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
          {authMode === "bootstrap" ? (
            <>
              <h1 className="text-2xl font-semibold tracking-tight text-white">
                Create first admin
              </h1>
              <p className="mt-2 text-sm text-slate-400">
                No admin user found. Set up the initial administrator.
              </p>
            </>
          ) : challenge ? (
            <>
              <h1 className="text-2xl font-semibold tracking-tight text-white">
                Two-factor sign-in
              </h1>
              <p className="mt-2 text-sm text-slate-400">
                Enter the code from your authenticator app, or one of your recovery codes.
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
        ) : challenge ? (
          <form onSubmit={handleCodeSubmit} className="space-y-5" noValidate>
            <div>
              <label
                htmlFor="mfa-code"
                className="mb-1.5 block text-ui font-medium text-slate-300"
              >
                Code
              </label>
              <input
                id="mfa-code"
                name="code"
                type="text"
                required
                autoFocus
                autoComplete="one-time-code"
                spellCheck={false}
                className="input font-mono tracking-widest"
                placeholder="123456"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                disabled={loading}
              />
              <p className="mt-1.5 text-xs text-slate-400">
                Lost your authenticator? Use a recovery code, or ask an administrator to reset two-factor sign-in.
              </p>
            </div>

            {error && <Callout tone="danger">{error}</Callout>}

            <Button
              type="submit"
              variant="accent"
              size="sm"
              loading={loading}
              disabled={loading}
              className="!h-10 w-full justify-center !text-sm"
            >
              {loading ? "Signing in…" : "Verify and continue"}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={loading}
              className="!h-10 w-full justify-center !text-sm"
              onClick={() => {
                setChallenge(null);
                setCode("");
                setError("");
              }}
            >
              Back to sign in
            </Button>
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
              {authMode === "login" && (
                <p className="mt-1.5 text-right text-xs">
                  <Link href="/forgot-password" className="text-cyan-300 hover:text-cyan-200">
                    Forgot password?
                  </Link>
                </p>
              )}
            </div>

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

            {authMode === "bootstrap" && setupCodeRequired && (
              <div>
                <label
                  htmlFor="setup-code"
                  className="mb-1.5 block text-ui font-medium text-slate-300"
                >
                  Setup code
                </label>
                <input
                  id="setup-code"
                  name="setup-code"
                  type="text"
                  required
                  autoComplete="off"
                  autoCapitalize="characters"
                  spellCheck={false}
                  className="input font-mono"
                  placeholder="XXXX-XXXX-XXXX-XXXX-XXXX"
                  aria-describedby="setup-code-hint"
                  value={setupCode}
                  onChange={(e) => setSetupCode(e.target.value)}
                  disabled={loading}
                />
                <p id="setup-code-hint" className="mt-1.5 text-xs text-slate-400">
                  The installer printed it when it finished; it is also
                  BOOTSTRAP_SETUP_CODE in the install directory&apos;s .env.
                </p>
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

            {ssoButtonVisible({ mode: authMode, setupCodeRequired }, oidc.enabled) && (
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
