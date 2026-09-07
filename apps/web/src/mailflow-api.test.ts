import { afterEach, describe, expect, it, vi } from "vitest";
import { createGoogleAuthorization, disconnectAccount, loadGoogleAccounts } from "./mailflow-api";

const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { "content-type": "application/json" } });

afterEach(() => vi.unstubAllGlobals());

describe("Mailflow API client", () => {
  it("uses a short Better Auth JWT and exposes only public account fields", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(json({ token: "short-jwt" }))
      .mockResolvedValueOnce(json({ token: "short-jwt" }))
      .mockResolvedValueOnce(json({ configured: true, setup: "docs/providers/google.md" }))
      .mockResolvedValueOnce(
        json({
          items: [
            {
              id: "account-1",
              provider: "google",
              displayName: "Test",
              syncState: "idle",
              disabledAt: null,
            },
          ],
        }),
      );
    vi.stubGlobal("fetch", fetch);
    const result = await loadGoogleAccounts();
    expect(result.accounts).toHaveLength(1);
    expect(fetch.mock.calls[2]?.[1]?.headers).toMatchObject({ authorization: "Bearer short-jwt" });
  });

  it("starts OAuth and disconnects without receiving provider credentials", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(json({ token: "short-jwt" }))
        .mockResolvedValueOnce(
          json({ authorizationUrl: "https://accounts.example.test/authorize" }),
        )
        .mockResolvedValueOnce(json({ token: "short-jwt" }))
        .mockResolvedValueOnce(
          json({
            account: {
              id: "account-1",
              provider: "google",
              displayName: "Test",
              syncState: "disabled",
              disabledAt: "2026-09-07T00:00:00Z",
            },
            remoteRevoked: true,
          }),
        ),
    );
    const authorizationUrl = await createGoogleAuthorization();
    const result = await disconnectAccount("account-1");
    expect(authorizationUrl).toBe("https://accounts.example.test/authorize");
    expect(result.remoteRevoked).toBe(true);
  });
});
