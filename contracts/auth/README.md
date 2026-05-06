# Auth Contract

Produced by `apps/portal`.

Consumed by:

- `apps/chat` verifier for legacy Chat audience checks
- `apps/home` verifier for Home audience checks
- `apps/kittypaw` as opaque bearer credentials

The daemon treats access tokens as opaque. Signature verification belongs to
resource servers.
