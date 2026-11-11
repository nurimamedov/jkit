# Read correctness, queue receipt and job configuration patch

Version: `0.10.1-ext.1`. Base: upstream `v0.10.1`
(`997117ccf995c70d3d8fb3e902a4c1443455a679`). The five extended patches
from `0.6.0-ext.4` are ported onto this release, preserving strict same-origin
authentication, private credentials, safe downloads, qualified comparisons,
queue receipts, bounded artifact stdout and exact-byte job configuration.
New upstream auth prune, input and inspect paths enforce the same private-file,
masking and bounded XML policies.
The module path and upstream MIT license are retained.

Changes and compatibility behavior are documented in [commands.md](commands.md).
In particular, `diff` now requires both builds' stage data and test reports.
It fails if either is unavailable. Consumers must handle this nonzero exit;
an unavailable comparison is not an empty comparison.

Build from a clean, committed checkout:

```sh
GOTOOLCHAIN=go1.26.0 GO111MODULE=on go vet ./...
GOTOOLCHAIN=go1.26.0 GO111MODULE=on go test ./... -count=1
gofmt -l .
JENKINS_PORT=19091 COMPOSE_PROJECT_NAME=jkit0101upgrade GOTOOLCHAIN=go1.26.0 GO111MODULE=on make integration-test
CGO_ENABLED=0 GOTOOLCHAIN=go1.26.0 GO111MODULE=on go build -trimpath \
  -ldflags '-s -w -X github.com/ysmaoui/jkit/cmd.version=0.10.1-ext.1' \
  -o dist/jkit .
dist/jkit --version
go version -m dist/jkit
```

Record the source commit, toolchain version, build flags, target platform,
checksums, and test results with each artifact. Keep `LICENSE` and dependency
notices with binary packages. Do not reuse the version for different source.

Keep the candidate beside the installed binary until deployment is approved.
Wrappers that enforce exact backend versions need explicit compatibility with
`0.10.1-ext.1`; reporting this build as unmodified `0.6.0` is incorrect.
