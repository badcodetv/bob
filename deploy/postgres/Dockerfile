# Bob's own Postgres. Not shared with any other app: it runs as the `postgres` service in
# deploy/compose.yml (the box) and deploy/compose.dev.yml (a laptop), and nothing outside Bob's
# stack can reach it.
#
# Published by hand, ONLY when this directory changes: ./stack publish-postgres. The tag is pinned
# in both compose files and is never ${TAG}, so deploying a new Bob leaves the database running —
# compose recreates only services whose image or definition changed.
#
# pgvector for memories; pgbackrest for the box's backups (the box's /etc/pgbackrest/pgbackrest.conf
# names a [bob] stanza; ops/apps/postgres/backup is the machinery). Nothing else.
FROM postgres:18.6-bookworm
# The PGDG apt repository is already configured in the official image.
RUN apt-get update && apt-get install -y --no-install-recommends \
      postgresql-18-pgvector \
      pgbackrest \
 && rm -rf /var/lib/apt/lists/*
COPY initdb/ /docker-entrypoint-initdb.d/
