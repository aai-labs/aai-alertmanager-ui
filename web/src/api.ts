const base = new URL("./", document.baseURI);
export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(new URL("api/" + path, base), {
    ...init,
    headers: {
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
    signal: AbortSignal.timeout(15000),
  });
  if (!res.ok) {
    let error = `Request failed (${res.status})`;
    try {
      const body = await res.json();
      if (typeof body.error === "string") error = body.error;
    } catch {
      /* Preserve HTTP error when proxy returns HTML. */
    }
    throw new Error(error);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}
