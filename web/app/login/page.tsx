"use client";

import { Suspense, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import Button from "@/components/ui/Button";
import { clearApiKey } from "@/lib/auth";
import { getOidcStatus } from "@/lib/api";
import type { OidcStatus } from "@/lib/types";

type BootstrapStatusResponse = {
  has_admin_users: boolean;
};

type ApiErrorResponse = {
  message?: string;
};

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

      if (!response.ok) {
        return await readErrorMessage(response, "Login failed");
      }

      // Ensure we don't fall back to API-key mode after admin login
      clearApiKey();

      router.push("/");
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
      setLoading(false);
    }
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
    <div className="flex min-h-screen items-center justify-center">
      <div className="pointer-events-none fixed inset-0 bg-gradient-to-br from-cyan-500/5 via-transparent to-sky-500/5" />

      <div className="relative w-full max-w-md p-8">
        <div className="rounded-2xl border border-white/[0.08] bg-slate-900/60 p-8 backdrop-blur-xl">
          <div className="mb-8 flex justify-center">
            <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-gradient-to-br from-cyan-400 to-cyan-600 shadow-lg shadow-cyan-500/20">
              <svg
                className="h-8 w-8 text-white"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth={2}
                  d="M12 11c1.657 0 3-1.343 3-3S13.657 5 12 5 9 6.343 9 8s1.343 3 3 3zm0 2c-2.67 0-8 1.34-8 4v2h16v-2c0-2.66-5.33-4-8-4z"
                />
              </svg>
            </div>
          </div>

          <div className="mb-8 text-center">
            {authMode === "bootstrap" ? (
              <>
                <h1 className="text-2xl font-semibold tracking-tight text-white">
                  Create first admin
                </h1>
                <p className="mt-2 text-sm text-slate-500">
                  No admin user found. Set up the initial administrator.
                </p>
              </>
            ) : (
              <>
                <h1 className="text-2xl font-semibold tracking-tight text-white">
                  Admin sign in
                </h1>
                <p className="mt-2 text-sm text-slate-500">
                  Use your admin credentials to access all tenants
                </p>
              </>
            )}
          </div>

          {authMode === "loading" ? (
            <div className="py-8 text-center text-sm text-slate-400">
              Checking admin setup...
            </div>
          ) : (
            <form
              onSubmit={
                authMode === "bootstrap"
                  ? handleBootstrapSubmit
                  : handleLoginSubmit
              }
              className="space-y-6"
            >
              <div>
                <label
                  htmlFor="username"
                  className="mb-2 block text-sm font-medium text-slate-400"
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
                  className="mb-2 block text-sm font-medium text-slate-400"
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

              {authMode === "bootstrap" && (
                <div>
                  <label
                    htmlFor="confirm-password"
                    className="mb-2 block text-sm font-medium text-slate-400"
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

              {error && (
                <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3">
                  <p className="text-sm text-rose-400">{error}</p>
                </div>
              )}

              <Button
                type="submit"
                variant="accent"
                size="sm"
                loading={loading}
                disabled={loading}
                className="w-full justify-center"
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
                    <div className="h-px flex-1 bg-white/[0.08]" />
                    <span className="text-xs uppercase tracking-wider text-slate-500">
                      or
                    </span>
                    <div className="h-px flex-1 bg-white/[0.08]" />
                  </div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    disabled={loading}
                    className="w-full justify-center"
                    onClick={() => {
                      clearApiKey();
                      window.location.href = "/api/v1/auth/oidc/start";
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
    </div>
  );
}
