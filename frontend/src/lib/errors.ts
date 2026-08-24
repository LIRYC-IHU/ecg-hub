import { Code, ConnectError } from "@connectrpc/connect";

/**
 * User-facing text for an error coming back from the API.
 *
 * Two shapes reach the UI: Connect errors, whose `message` is formatted by
 * `@connectrpc/connect` as "[code] message", and the REST handlers' JSON body
 * `{code, message}` surfaced as `.error`. Neither prefix means anything to a
 * clinician, so the code is stripped and only the sentence is shown; the code
 * stays visible in the network tab and in the server log for debugging.
 *
 * Returns `fallback` when the error carries no text of its own.
 */
export function errorMessage(err: unknown, fallback: string): string {
  const source = err as { error?: unknown; message?: unknown } | null | undefined;
  const raw = String(source?.error ?? source?.message ?? "");
  return raw.replace(/^\[[a-z_]+\]\s*/i, "").trim() || fallback;
}

/**
 * True when the error is a Connect error carrying `code`.
 *
 * ConnectError.code is the numeric Code enum, not the wire string, so a
 * comparison against a literal like "failed_precondition" — or against a
 * REST-era code such as "ROLE_HAS_USERS" — silently never matches and the
 * caller falls through to its generic message.
 */
export function hasCode(err: unknown, code: Code): boolean {
  return err instanceof ConnectError && err.code === code;
}
