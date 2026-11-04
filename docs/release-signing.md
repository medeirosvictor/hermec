# Release signing

Release builds update themselves only from artifacts that the project signed.
CI signs the combined `SHA256SUMS.txt` of each release with an Ed25519 key and
publishes the detached signature `SHA256SUMS.txt.sig`. The app embeds the
matching public key (`update.PublicKey`), and installs an update only when

1. the signature of `SHA256SUMS.txt` verifies under that key, and
2. the SHA-256 of the downloaded artifact equals its line in that file.

The signature is base64 of `ed25519.Sign(priv, "hermec-release-v2\x00" || tag ||
"\x00" || sums_bytes)`. The fixed prefix keeps it from being valid for
anything else, and the release tag (for example `v0.8.0`) is part of the signed
bytes, so a validly signed `SHA256SUMS.txt` of an older release cannot be
replayed as a newer one: the app verifies against the tag it is installing,
requires that tag to be a strict `vX.Y.Z` newer than the running build, and
`update.sh` requires the tag to be newer than the installed server.

The app fails closed: with no valid signature it never installs an update (it
still shows the banner and links to the Releases page). The release workflow
refuses to publish a release that no installed app could verify: a tagged
release fails at "Sign checksums" if the `HERMEC_RELEASE_KEY` secret is missing,
and at "Verify signature against the embedded public key" if the secret does
not match `update.PublicKey`.

## Key setup

The public key is embedded in `update/verify.go` and the private key is the
repository's `HERMEC_RELEASE_KEY` Actions secret. To create a new key pair
(first setup, or a rotation below):

1. Generate the key on a trusted machine (not in CI):

       go run ./cmd/hermec-release keygen -out release.key

   This writes the base64 private seed to `release.key` (mode 0600, and it
   refuses to overwrite an existing file) and prints the base64 **public** key.
2. Set the printed public key as the value of `PublicKey` in
   `update/verify.go` and commit it. It is public; it is meant to be in the
   source and the binaries.
3. In the GitHub repository, add an Actions secret named `HERMEC_RELEASE_KEY`
   whose value is the content of `release.key` (Settings, Secrets and
   variables, Actions, New repository secret).
4. Keep `release.key` offline (a password manager or an encrypted backup) and
   delete the plaintext copy from the machine you generated it on. Losing it
   only means rotating (below); leaking it means anyone can sign an update.

## Checking a release by hand

    go run ./cmd/hermec-release verify -tag v0.8.0 SHA256SUMS.txt        # embedded key
    go run ./cmd/hermec-release verify -tag v0.8.0 -pub <base64> SHA256SUMS.txt

`-tag` is required (CI signs with `sign -tag "$TAG"`) and `verify` prints the
tag it checked. `SHA256SUMS.txt.sig` must sit next to the sums file. Then `sha256sum -c
--ignore-missing SHA256SUMS.txt` checks the downloaded artifacts.

## Rotating the key

Installed apps trust only the key compiled into them, so a rotation needs one
release that is still signed with the **old** key:

1. Generate the new key as above.
2. Release N: change `update.PublicKey` to the **new** public key, but sign
   release N with the **old** key (the `HERMEC_RELEASE_KEY` secret is still the
   old one). Apps running N-1 verify it, update, and now trust the new key.
   The workflow's verify step would fail here (it checks against the embedded
   new key), so run that one release with the verify step temporarily
   pointing at the old public key (`verify -tag "$TAG" -pub <old>`).
3. Replace the `HERMEC_RELEASE_KEY` secret with the new key. Release N+1 and
   later are signed with it.

If the old key is compromised, there is no safe in-app path: publish a new
build from the Releases page with the new key and tell users to reinstall it by
hand.

## Dedicated servers

`update.sh` verifies by default, using the `hermec-release` that `install.sh`
put in the root-owned `/usr/local/lib/hermec/` (the key compiled into that
tool), never a tool from the archive being verified; see
[docs/dedicated-hosting.md](dedicated-hosting.md#updating-a-signed-release).
