# SkyShield deployment maintenance

For SkyShield deployments the web application has no update endpoints. The
host runs `./update.sh` to fetch and install the official release. When the
script creates the local maintenance marker, the Server blocks protected
writes while the script checks active work and stops the application.

The upstream LunaFox deployment path retains its own upgrade service. For
SkyShield release and recovery procedures, see `docs/16-系统更新.md`.
