# CLAUDE.md

Guidance for Claude Code in this repository. Read `README.md` for how the code works.

## Where this runs — BadCode's box

Bob runs on **BadCode's box**: one OVH server, `box.badcode.tv`
(`217.182.193.214`), where every app is a `docker compose` stack under `/srv/apps/` with Caddy in front.
It replaces the old Google Kubernetes cluster.

- **Now (2026-10-23):** **live on the box** at `https://bob.box.badcode.tv` (no cluster copy; nothing to switch).
- **Deploy:** `./stack deploy` publishes HEAD and ships it; `./stack deploy <tag>` ships an older tag (the
  rollback). `deploy/compose.yml` is exactly what runs on the box. Kubernetes manifests and
  `manual_deploy.sh` here are the old cluster's and go when it closes.
- **Data:** the database `bob` in the box's **one shared Postgres 18** (ops `apps/postgres`), reached
  as `postgres:5432` on the `box-db` network. `BOB_DATABASE_URL` in `.env.box` is written and rotated
  from ops: `./stack db create|password bob --env-file ~/projects/badcode/bob/.env.box --url-var
  BOB_DATABASE_URL`. The box backs it up. On the laptop, `deploy/compose.dev.yml` runs its own Postgres.
- **Secrets:** its secrets are git-ignored in **this** repository: `.env.box` (the box's) and `.env` (the laptop's). The master copy is Kai's password manager, entry "OVH box". Never put them in ops, never commit them.
- **The box itself** (Caddy, backups, the map of every app, the switch playbook) is documented in the
  private ops repository `binocarlos/ops`, cloned at `~/projects/badcode/ops`. Read its `CLAUDE.md`
  and `README.md` before touching the box. A new route in Caddy or a new backup is a change made in
  ops, written down there in the same session.
- **From a new computer:** clone this repository; copy `.env.box` from the password manager; `gcloud auth login`
  and `gcloud auth configure-docker europe-west1-docker.pkg.dev` (so `./stack publish` can push images); check `ssh ubuntu@box.badcode.tv` works. Then
  `./stack deploy`.
