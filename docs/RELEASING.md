# Releasing

`abdim-cli` publishes immutable Git tags as GitHub Release assets. The initial
release workflow produces Linux amd64/arm64 archives plus `SHA256SUMS`. It
does not deploy a daemon or receive deployment credentials. macOS and Windows
are not v0.1.0 release targets: the isolated provider launcher is currently a
Linux deployment boundary.

## GitHub Setup

Create the repository secret `ABDIM_SDK_READ_TOKEN`. It must be a fine-grained
GitHub token with read-only Contents access to `abd-im/abd-im-sdk-core`; it is
used only to download the private Go module. Keep the default `GITHUB_TOKEN`
with Contents write access for the release workflow.

Create the protected `openim-integration` environment. Define these
environment variables there:

| Variable | Value |
| --- | --- |
| `ABDIM_OPENIM_API_ADDR` | `https://2.alissa.xin/api` for the current ABD deployment. |
| `ABDIM_OPENIM_WS_ADDR` | `wss://2.alissa.xin/msg_gateway` for the current ABD deployment. |
| `ABDIM_OPENIM_USER_ID` | Controlled sender's canonical OpenIM user ID. |
| `ABDIM_OPENIM_GROUP_CREATE_MEMBER_ID` | A distinct controlled member ID. |
| `ABDIM_OPENIM_MESSAGE_SEND_RECIPIENT_ID` | A distinct controlled recipient ID. |
| `ABDIM_OPENIM_PLATFORM_ID` | The platform that issued the short-lived IM token; current Linux release tests use `7`. |

Add `ABDIM_OPENIM_TOKEN` as an environment secret. It must be a short-lived
token for the configured platform, never an account password. Add
`ABDIM_SDK_READ_TOKEN` to the same environment if repository secrets are not
available to protected environments.

`ci.yml` runs on internal pull requests and pushes to `main`. Fork pull
requests are intentionally skipped because GitHub does not expose private SDK
credentials to untrusted workflow code. `controlled-integration.yml` is
manual-only because it creates a disposable group and sends a controlled test
message.

## Release Candidate

Before tagging, require a green CI run, manually approve and run the controlled
OpenIM workflow, and complete the root daemon/Codex inbound-reply canary from
[`CONNECTOR.md`](CONNECTOR.md). The GitHub release is a software artifact; it
does not replace that deployment canary.

Create and push the release candidate tag from the verified `main` commit:

```bash
git tag -a v0.1.0-rc1 -m "v0.1.0-rc1"
git push origin v0.1.0-rc1
```

The `Release` workflow detects the prerelease suffix, creates or updates the
GitHub prerelease, uploads the archives and `SHA256SUMS`, and generates release
notes. Re-running it with the same tag replaces only the generated assets.

Verify a downloaded archive before installation:

```bash
sha256sum -c SHA256SUMS
```

Use the verified binary with the root-owned provider configuration and
separate provider UID described in [`CONNECTOR.md`](CONNECTOR.md). Do not run a
release daemon from the development checkout or with a browser/mobile token
issued for a different platform.
