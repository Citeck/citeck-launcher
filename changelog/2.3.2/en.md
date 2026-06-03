## Fixes

- Admin-password reset and keycloak `init.sh` now self-heal when the
  stored `citeck` SA password is out of sync with Keycloak — falls
  back to `kc.sh bootstrap-admin user` to re-sync the SA without
  needing any existing credentials.
