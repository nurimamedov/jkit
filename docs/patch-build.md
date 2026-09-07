# Read correctness, queue receipt and job configuration patch

Version: `0.6.0-ext.4`. Base: upstream `v0.6.0`
(`4b244fa169f08a6486f98b4a58886dfe229ea7e0`), plus the URL/build-target fix
from `be09c680bdab7dec33ffa8684a86a51dcea6c427` and the read correctness patch.
This candidate also includes the security patch from
`b445c2c0eb0ca26cb4a9c52ff3dbf35dda60e3d1` and the bounded queue receipt API.
This version adds bounded exact-byte job configuration reads and single-POST
updates on top of `bf52411e874469f8e6b6acf15c2e618ebb87572f`.
The module path and upstream MIT license are retained.

Changes and compatibility behavior are documented in [commands.md](commands.md).
In particular, `diff` now requires both builds' stage data and test reports.
It fails if either is unavailable. Consumers must handle this nonzero exit;
an unavailable comparison is not an empty comparison.

Build from a clean, committed checkout:

```sh
GO111MODULE=on go vet ./...
GO111MODULE=on go test ./... -count=1
gofmt -l .
make integration-test
CGO_ENABLED=0 GO111MODULE=on go build -trimpath \
  -ldflags '-s -w -X github.com/ysmaoui/jkit/cmd.version=0.6.0-ext.4' \
  -o dist/jkit .
dist/jkit --version
go version -m dist/jkit
```

Record the source commit, toolchain version, build flags, target platform,
checksums, and test results with each artifact. Keep `LICENSE` and dependency
notices with binary packages. Do not reuse the version for different source.

Keep the candidate beside the installed binary until deployment is approved.
Wrappers that enforce exact backend versions need explicit compatibility with
`0.6.0-ext.4`; reporting this build as unmodified `0.6.0` is incorrect.
