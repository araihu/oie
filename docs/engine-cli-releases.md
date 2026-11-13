# Engine contract and oiectl releases

| Component | Git tag | Version source |
| --- | --- | --- |
| Oiê application | `vX.Y.Z` | Application release |
| Engine OpenAPI contract | `openapi/engine/vX.Y.Z` | `info.version` in `api/openapi.yaml`, with a `v` prefix in the tag |
| oiectl | `cli/vX.Y.Z` | Engine contract major and minor, with its own patch |

The `openapi/engine/` namespace is read by the [Arai Hû OpenAPI catalog](https://oapi.araihu.com).
Push an immutable spec tag on the commit containing the reviewed contract and
its CLI overlay. The tag itself publishes the spec version in the catalog.
The same tag starts the [oiectl release workflow](../.github/workflows/release-oiectl.yml).

```sh
git tag -a openapi/engine/v0.30.0 COMMIT -m 'Engine OpenAPI 0.30.0'
git push origin refs/tags/openapi/engine/v0.30.0
```

The workflow regenerates `oiectl` using the tools pinned at the tagged commit
and verifies the tag against `info.version`. A new major/minor starts at patch
`0`. Later releases use one more than the highest CLI patch in that series.
Spec prereleases also produce CLI prereleases. Each release has prebuilt Linux
x86-64 and macOS Apple Silicon archives, a corresponding source archive, and
`SHA256SUMS`. The release notes and `oiectl --version` show the exact spec tag.
For example, spec `openapi/engine/v0.30.1` can produce this output if two CLI
patches have already been used in the `0.30` series:

```text
oiectl version v0.30.2 (spec openapi/engine/v0.30.1)
```

The CLI tag points to a commit with the generated code and release metadata.
GitHub downloads and the versioned `go install` command in the release notes
use that same source. CLI releases leave the repository's latest application
release selection in place.

To release a CLI-only fix, run **Release oiectl** on the branch or tag with the
fix, enter the published `spec_tag`, and select `cli_patch`. The selected source
must contain the exact tagged engine contract. To retry the automated spec
release, rerun the failed job or dispatch the workflow with the same spec tag
and `cli_patch` off. For a CLI-only patch, rerun the same job to reuse the
original request. Retries after the CLI tag was pushed reuse that tag and can
complete a partial GitHub release.
