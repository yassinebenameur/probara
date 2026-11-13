// The one place that knows what a failed API call looks like.
//
// The client throws ApiRequestError for a JSON error body (`{error, message}`)
// and a plain Error for transport or parse failures. ApiRequestError is a real
// Error, so `err instanceof Error` and `err.message` work on it everywhere; it
// keeps the machine-readable `error` code the server sent.

export class ApiRequestError extends Error {
  readonly error: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = 'ApiRequestError';
    this.error = code;
  }
}

/** Builds the error for a non-2xx JSON body, tolerating bodies with no `error` code. */
export function apiErrorFromBody(data: unknown): ApiRequestError {
  const body = (data && typeof data === 'object' ? data : {}) as { error?: unknown; message?: unknown };
  const message = typeof body.message === 'string' && body.message ? body.message : undefined;
  if (typeof body.error === 'string' && body.error) {
    return new ApiRequestError(body.error, message ?? body.error);
  }
  return new ApiRequestError('unknown_error', message ?? 'An error occurred');
}

export const DEFAULT_ERROR_MESSAGE = 'The request failed. Please retry.';

/** The message to show for anything a `catch` can receive; `fallback` when it carries none. */
export function errorMessage(err: unknown, fallback: string = DEFAULT_ERROR_MESSAGE): string {
  if (err && typeof err === 'object' && 'message' in err) {
    const message = (err as { message?: unknown }).message;
    if (typeof message === 'string' && message) return message;
  }
  return fallback;
}

/** The server's error code (`not_found`, `conflict`, …), if the failure carried one. */
export function errorCode(err: unknown): string | undefined {
  if (err && typeof err === 'object' && 'error' in err) {
    const code = (err as { error?: unknown }).error;
    if (typeof code === 'string' && code) return code;
  }
  return undefined;
}
