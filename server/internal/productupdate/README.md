# SkyShield deployment updates

Authenticated `/v1/deploymentUpdate`, `/v1/system:checkForUpdates` and
`/v1/deploymentUpdate:apply` use the private host updater socket. Each action
checks the current `auth_user` superuser state. Client inputs contain only an
idempotency UUID, a checked manifest digest and explicit confirmation.

`scripts/system-update.py` owns the fixed HTTPS channel, Git source and pinned
images; commands, repositories and paths cannot be selected by an API client.
Its durable journal and single-deployment locks survive application restarts.
A short maintenance marker rejects new application writes before handoff;
background task activity is checked again before services stop.

The old LunaFox operation service remains available only to legacy wiring
without `SKYSHIELD_DEPLOYMENT_MODE`. SkyShield modes do not instantiate its
manifest loader or recovery job. The old active-operation resource returns no
operation to avoid reviving an unrelated browser lock.

Release generation, first-install bootstrap and recovery limits are described
in `docs/16-系统更新.md`. Never interpret a successful image pull or syntax check
as evidence that a live upgrade or database restore was tested.
