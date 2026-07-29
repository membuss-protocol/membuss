---
id: grpc-api
title: Daemon gRPC API Specification (membuss.proto)
sidebar_label: gRPC API
---

# Daemon gRPC API Specification (`proto/membuss.proto`)

The `membuss` CLI communicates with the local daemon via gRPC listening on `127.0.0.1:50051`.

---

## Service Definition

```protobuf
syntax = "proto3";

package membuss;

service MembussDaemon {
  rpc Add(AddRequest) returns (AddResponse);
  rpc Get(GetRequest) returns (stream GetResponse);
  rpc Stat(StatRequest) returns (StatResponse);
  rpc Seal(SealRequest) returns (SealResponse);
  rpc Unseal(UnsealRequest) returns (UnsealResponse);
  rpc ListPeers(PeersRequest) returns (PeersResponse);
  rpc RunGC(GCRequest) returns (GCResponse);
}
```
