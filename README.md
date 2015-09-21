# acpush
acpush is a library and CLI binary to upload App Container images (ACIs).

It takes as input an
[ACI](https://github.com/appc/spec/blob/master/SPEC.md#app-container-image)
file, an
[ASC](https://github.com/coreos/rkt/blob/master/Documentation/signing-and-verification-guide.md)
file, and an [App Container
Name](https://github.com/appc/spec/blob/master/spec/types.md#ac-name-type) (i.e.
`quay.io/coreos/etcd`). Meta discovery is performed via the provided name to
determine where to push the image to.

## Build

Building acpush requires go to be installed on the system. With that, the
project can be built with either:

```
go get github.com/appc/acpush
```

or

```
git clone https://github.com/appc/acpush.git
cd acpush
go get -d ./...
go build
```

## Auth

acpush reads rkt's config files to determine what authentication is necessary
for the push. See [rkt's
documentation](https://coreos.com/rkt/docs/latest/configuration.html) for
details on the location and contents of these configs.

## Usage

See `acpush --help` for details on accepted flags.
