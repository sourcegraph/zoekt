# Sourcegraph indexserver protobuf definitions

This directory contains protobuf definitions for the indexserver gRPC API.

To generate the Go code, run this script from the repository root:

```sh
./gen-proto.sh
```

Note: this script will regenerate all protos in the project, not just the ones in this directory.
