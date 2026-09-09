# Release supply-chain evidence

Mailflow release evidence starts from an immutable SemVer tag and the complete
commit recorded in each evidence manifest. A release is acceptable only when
the tag, commit, artifact checksum, signing identity, SBOM and provenance agree.
The protected workflows leave the GitHub release as a draft; publishing it is
a separate release decision.

## Trust and authority

Pull-request jobs have repository read access only. They can compile the two
container architectures, build an ad-hoc macOS application and run policy and
tamper tests, but they have no environment, package-write, attestation-write or
OIDC signing authority. Tag jobs use either the `container-release` or
`desktop-release` protected environment and request only the permissions used
by their publishing steps.

Keyless Sigstore certificates must identify this repository, the exact release
workflow and its actual Git ref. Apple Developer ID, notarization and the Tauri
updater signature remain additional independent controls for macOS; none of
their private material is included in the release directory.

## Evidence sets

The container evidence set contains the closed five-image digest manifest, one
CycloneDX JSON SBOM per image, `container-release-evidence.json`, its Sigstore
bundle, GitHub provenance bundle and `container-SHA256SUMS`. Each image digest
also carries a Cosign signature,
BuildKit SBOM and provenance plus GitHub provenance and SBOM attestations in
the registry.

The native evidence set contains the universal DMG and updater archive, the
Tauri updater signature and update manifest, a CycloneDX JSON SBOM,
`native-release-evidence.json`, one Sigstore bundle for each installable
artifact, downloadable GitHub provenance and SBOM bundles, and
`macos-SHA256SUMS`. GitHub attaches provenance to every checksummed file
and associates the SBOM with the DMG and updater archive. Apple signatures and
notarization tickets are verified before the evidence is generated.

Both evidence manifests use safe basenames, exact SHA-256 values and a full
commit. `scripts/release-evidence.ts verify` rejects missing, empty, linked,
renamed or modified local subjects, malformed OCI digests and invalid
CycloneDX documents. Automated tests change artifact and SBOM bytes to prove
that verification fails closed.

## Independent verification

Download a draft or published release into a new directory. Verify its checksum
file before trusting names from the evidence manifest:

```bash
shasum -a 256 -c macos-SHA256SUMS
bun scripts/release-evidence.ts verify native-release-evidence.json .
```

For a native artifact, verify the keyless bundle and GitHub provenance using
the tag shown in the evidence manifest:

```bash
cosign verify-blob \
  --bundle Mailflow_1.0.0_universal.dmg.sigstore.json \
  --certificate-identity \
  'https://github.com/Tutitoos/mailflow/.github/workflows/desktop-release.yml@refs/tags/v1.0.0' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  Mailflow_1.0.0_universal.dmg

gh attestation verify Mailflow_1.0.0_universal.dmg \
  --repo Tutitoos/mailflow
```

For containers, first verify `container-release-evidence.json` and its bundle,
then run the digest-based Cosign and GitHub commands in
[`container-releases.md`](container-releases.md) for all five references. Never
substitute a mutable tag during verification.

If any check fails, do not install, publish, retag or repair the evidence set.
Delete the untrusted download, inspect the protected workflow run and produce a
new patch or prerelease from a reviewed commit. Rollback uses the previous
fully verified evidence set and, when database compatibility requires it, the
matching tested backup.

## Resumen en español

Cada release queda ligada a un tag, commit, checksums SHA-256, SBOM CycloneDX,
firmas Sigstore y provenance verificable. Las PR solo compilan y prueban: no
reciben credenciales, entornos protegidos ni autoridad OIDC. Antes de instalar
hay que verificar todos los archivos y digests de forma independiente. Si una
comprobación falla, no se corrige ni se sobrescribe la release; se genera una
nueva versión revisada o se vuelve al conjunto de evidencias anterior.
