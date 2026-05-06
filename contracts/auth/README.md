# Auth Contract

Produced by `apps/portal`.

Consumed by:

- `apps/chat` verifier for legacy Chat audience checks
- `apps/space` verifier for Space audience checks
- `apps/kittypaw` as opaque bearer credentials

The daemon treats access tokens as opaque. Signature verification belongs to
resource servers.
