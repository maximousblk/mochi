# Admission

Who may connect to this machine's Tailcat server.

## Identity

Each machine has one secret: an Ed25519 seed stored in the state database. Its
public half, the **machine key** (64 hex characters, shown on the **This device** page), is the
machine's identity. The Tailcat node keys are derived from the seed, separately
for serving and dialing, because one node key cannot do both at once without
losing relay traffic.

## Trusted keys

A node may use the server only while a trusted machine key has approved it.
Trusted keys are this machine's own key and the keys listed in the embedded
[`bootstrap.json`](BOOTSTRAP.md). A key removed from the bootstrap list loses its
approvals at the next start.

## Probation

A node the server has not seen gets one of 32 probation slots for 20 seconds.
During probation it can reach only the proof port. Anyone who knows the server's
public key can take slots, with or without the pre-shared key, so a flood can
delay first contact for new nodes. Approved nodes are not affected.

## Certificates

A client presents a certificate to the proof port. A valid certificate approves
the node until it expires. Later connections need no certificate.

Create one on a machine whose key a host trusts:

```sh
mochi issue --node nodekey:... --id alice --ttl 12h --out alice.cert
```

`issue` signs with this machine's own key unless `--key file` names a file that
holds a CA's private key as hex (a 32-byte seed or a 64-byte key).

The client pastes the certificate under **Certificates** in Settings. A host
that refuses a certificate lists the CAs it trusts, so the client can see why.

## Pending approval

Under **Pending approval** on the **This device** page, **Approve** signs a 24-hour certificate
with this machine's own key. The host renews it, so the approval lasts until
**Block**.

## Limits

- Approval is all or nothing. An approved node may use every service the server
  offers. Per-service policy is not implemented.
