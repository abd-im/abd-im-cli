# Releasing

`abdim-cli` publishes immutable Git tags as GitHub Release assets. The initial
release workflow produces Linux amd64/arm64 and macOS amd64/arm64 archives
plus `SHA256SUMS`. It does not deploy a daemon or receive deployment
credentials. Windows is not a v0.1.0 release target because the current
provider bridge uses Unix stdio/socket behavior.

## GitHub Setup

Create the repository secret `ABDIM_SDK_READ_TOKEN`. It must be a fine-grained
GitHub token with read-only Contents access to `abd-im/abd-im-sdk-core`; it is
used only to download the private Go module. Keep the default `GITHUB_TOKEN`
with Contents write access for the release workflow.

Configure it in the `abd-im-cli` repository at **Settings -> Secrets and
variables -> Actions -> New repository secret**. If the workflow shows an empty
`SDK_READ_TOKEN` and exits in `Configure private SDK access`, the secret is
missing or unavailable to that event. Re-running the push does not fix it; add
the secret, verify organization/SSO access to the private SDK repository, then
re-run the failed workflow.

Create the protected `openim-integration` environment. Define these
environment variables there:

| Variable | Value |
| --- | --- |
| `ABDIM_OPENIM_API_ADDR` | `https://2.alissa.xin/api` for the current ABD deployment. |
| `ABDIM_OPENIM_WS_ADDR` | `wss://2.alissa.xin/msg_gateway` for the current ABD deployment. |
| `ABDIM_OPENIM_USER_ID` | Controlled sender's canonical OpenIM user ID. |
| `ABDIM_OPENIM_GROUP_ID` | A pre-provisioned group visible to the controlled sender. |
| `ABDIM_OPENIM_MEMBER_QUERY` | A member ID or name query that matches that group. |
| `ABDIM_OPENIM_CONVERSATION_ID` | A pre-provisioned conversation used by bounded message reads. |
| `ABDIM_OPENIM_MESSAGE_ID` | A server message ID present in the latest 100 messages of that conversation. |
| `ABDIM_OPENIM_MESSAGE_QUERY` | Text matching a message in that same bounded window. |
| `ABDIM_OPENIM_AFTER_MESSAGE_ID` | A server message ID used as the exclusive lower read boundary. |
| `ABDIM_OPENIM_FRIEND_USER_ID` | A pre-provisioned friend used by read-only social tests. |
| `ABDIM_OPENIM_FRIEND_QUERY` | An ID, nickname, or remark matching that friend. |
| `ABDIM_OPENIM_BLACKLIST_USER_ID` | A pre-provisioned blacklist entry used by read-only social tests. |
| `ABDIM_OPENIM_GROUP_CREATE_MEMBER_ID` | A distinct controlled member ID. |
| `ABDIM_OPENIM_MESSAGE_SEND_RECIPIENT_ID` | A distinct controlled recipient ID. |
| `ABDIM_OPENIM_PLATFORM_ID` | The platform that issued the short-lived IM token; current release tests use `7`. |

Add `ABDIM_OPENIM_TOKEN` and `ABDIM_OPENIM_SECONDARY_TOKEN` as environment
secrets for the primary sender and `ABDIM_OPENIM_MESSAGE_SEND_RECIPIENT_ID`.
They must be short-lived tokens for the configured platform, never account
passwords. The workflow reuses these two controlled identities for group,
friend, and blacklist mutation fixtures. Add `ABDIM_SDK_READ_TOKEN` to the same
environment if repository secrets are not available to protected environments.

`ci.yml` runs on internal pull requests and pushes to `main`. Fork pull
requests are intentionally skipped because GitHub does not expose private SDK
credentials to untrusted workflow code. `controlled-integration.yml` is
manual-only because it validates every `available` read/action family and
mutates disposable account state. Missing fixtures fail during configuration
validation rather than allowing an integration test to skip.

## Release Candidate

Before tagging, require a green CI run, manually approve and run the controlled
OpenIM workflow, and complete both current-user daemon/ACP Agent direct-message
canaries from [`CONNECTOR.md`](CONNECTOR.md). Verify that the default run exposes
no `abdim` tools, then enable inbound tools and verify discovery plus one read
and one disposable write before disabling them again. In both modes, verify
that a group message creates no run. The GitHub release is a software artifact;
it does not replace that deployment canary.

For a public release, add the project owner's selected license as `LICENSE`.
The repository currently has no license, so it should be treated as a release
candidate until that product/legal choice is made.

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

Use the verified binary as the same user whose selected Agent is logged in. Put
its fixed entry point on that user's `PATH`, run `abdim setup`, and complete the one-time
setup described in [`CONNECTOR.md`](CONNECTOR.md). Do not use `sudo`, a
provider configuration file, a separate provider UID, or a manually copied
browser/mobile token. Do not run a release daemon from the development checkout.
