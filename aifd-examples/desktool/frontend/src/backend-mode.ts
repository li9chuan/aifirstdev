// Only an explicit browser preview may use simulated data. Native load errors
// must reach the caller instead of being replaced with a successful demo login.
export async function selectBackend<T>(
  mode: string,
  loadNative: () => Promise<T>,
  preview: T,
): Promise<T> {
  return mode === "preview" ? preview : loadNative();
}

// A missing native transport may leave Bootstrap pending instead of rejecting.
export async function withStartupTimeout<T>(pending: PromiseLike<T>, timeoutMs = 15000): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      pending,
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error("Desktop backend startup timed out")), timeoutMs);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}
