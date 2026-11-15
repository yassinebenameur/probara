// First-admin setup on the login page. The API reports whether an admin
// exists and whether creating the first one needs the install's one-time
// setup code (BOOTSTRAP_SETUP_CODE, printed by scripts/install.sh and kept in
// its .env); it never sends the code itself.

export type BootstrapStatus = {
  has_admin_users: boolean;
  setup_code_required?: boolean;
};

export type BootstrapView = {
  mode: 'login' | 'bootstrap';
  /** Show and require the "Setup code" field. */
  setupCodeRequired: boolean;
};

export function bootstrapView(status: BootstrapStatus): BootstrapView {
  if (status.has_admin_users) {
    return { mode: 'login', setupCodeRequired: false };
  }
  return { mode: 'bootstrap', setupCodeRequired: status.setup_code_required === true };
}

/** The POST /users/bootstrap/first body; the code is sent only when required. */
export function bootstrapRequestBody(
  username: string,
  password: string,
  setupCode: string,
  setupCodeRequired: boolean,
): { username: string; password: string; setup_code?: string } {
  const body: { username: string; password: string; setup_code?: string } = {
    username: username.trim(),
    password,
  };
  if (setupCodeRequired) {
    body.setup_code = setupCode.trim();
  }
  return body;
}
