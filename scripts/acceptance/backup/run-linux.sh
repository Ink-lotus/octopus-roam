#!/usr/bin/env bash
# Run the B01 database checks with disposable, loopback-only SQL containers.
set -euo pipefail
umask 077
task_root=${1:?absolute isolated task directory required}
case "$task_root" in
  /data/home/roam-backup-b01-20[0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9]) ;;
  *) echo 'Unexpected acceptance task directory.' >&2; exit 1 ;;
esac
cd "$task_root"
sha256sum --check inputs.sha256
pg_name=roam-backup-b01-pg18
mysql_name=roam-backup-b01-mysql84
task_label=backup-b01
pg_image=postgres@sha256:e923a22ac074261dd73970c57edf659475bf0497cb9456f17d98ed7cdd3ca913
mysql_image=mysql@sha256:72c368d898861f3b3a07af0e6d690400f44f048799ed46822783d9224cc2c86d
for name in "$pg_name" "$mysql_name"; do
  if docker container inspect "$name" >/dev/null 2>&1; then
    echo 'An acceptance container already exists; refusing to adopt it.' >&2
    exit 1
  fi
done
for report in postgres-report.json mysql-report.json sqlite-linux-report.json legacy-report.json upgrade-report.json restored-linux.sqlite; do
  test ! -e "$report"
done
docker image inspect --format '{{json .RepoDigests}}' "$pg_image" "$mysql_image"
chmod 700 backup-linux-amd64 legacy-repro-linux-amd64
pg_created=false
mysql_created=false
cleanup() {
  local result=$?
  trap - EXIT
  if [ "$pg_created" = true ]; then
    docker stop --time 25 "$pg_name" >/dev/null || result=1
    docker rm --volumes "$pg_name" >/dev/null || result=1
  fi
  if [ "$mysql_created" = true ]; then
    docker stop --time 25 "$mysql_name" >/dev/null || result=1
    docker rm --volumes "$mysql_name" >/dev/null || result=1
  fi
  unset ROAM_REPRO_DB_PASSWORD ROAM_BACKUP_TEST_DSN POSTGRES_PASSWORD MYSQL_PASSWORD MYSQL_ROOT_PASSWORD
  local remaining
  remaining=$(docker ps -a --filter "label=roam.task=$task_label" --format '{{.Names}}') || result=1
  if [ -n "$remaining" ]; then result=1; fi
  printf 'cleanup_exit=%s remaining_owned_containers=%s\n' "$result" "${remaining:-none}"
  exit "$result"
}
trap cleanup EXIT
export ROAM_REPRO_DB_PASSWORD
ROAM_REPRO_DB_PASSWORD=$(python3 -c 'import secrets; print(secrets.token_hex(32))')
export POSTGRES_PASSWORD="$ROAM_REPRO_DB_PASSWORD"
export MYSQL_PASSWORD="$ROAM_REPRO_DB_PASSWORD"
export MYSQL_ROOT_PASSWORD
MYSQL_ROOT_PASSWORD=$(python3 -c 'import secrets; print(secrets.token_hex(32))')
docker run --detach --name "$pg_name" --label "roam.task=$task_label" --cpus=2 --memory=1024m \
  --publish 127.0.0.1:15432:5432 --tmpfs /var/lib/postgresql:rw,nosuid,nodev,size=536870912 \
  --env POSTGRES_PASSWORD --env POSTGRES_USER=roam_repro --env POSTGRES_DB=roam_backup_repro "$pg_image" >/dev/null
pg_created=true
docker run --detach --name "$mysql_name" --label "roam.task=$task_label" --cpus=1 --memory=1536m \
  --publish 127.0.0.1:13306:3306 --tmpfs /var/lib/mysql:rw,nosuid,nodev,size=805306368 \
  --env MYSQL_ROOT_PASSWORD --env MYSQL_PASSWORD --env MYSQL_USER=roam_repro --env MYSQL_DATABASE=roam_backup_b01 "$mysql_image" >/dev/null
mysql_created=true
pg_ready=false
mysql_ready=false
for attempt in $(seq 1 90); do
  if docker exec "$pg_name" pg_isready -U roam_repro -d roam_backup_repro >/dev/null 2>&1; then pg_ready=true; fi
  if docker exec "$mysql_name" sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" mysql --protocol=TCP --host=127.0.0.1 --user="$MYSQL_USER" --database="$MYSQL_DATABASE" --execute="SELECT 1"' >/dev/null 2>&1; then mysql_ready=true; fi
  if [ "$pg_ready" = true ] && [ "$mysql_ready" = true ]; then break; fi
  sleep 2
done
if [ "$pg_ready" != true ] || [ "$mysql_ready" != true ]; then echo 'Database readiness timed out.' >&2; exit 1; fi
printf 'kernel=%s architecture=%s docker=%s\n' "$(uname -r)" "$(uname -m)" "$(docker version --format '{{.Server.Version}}')"
docker exec "$pg_name" psql -U roam_repro -d roam_backup_repro -v ON_ERROR_STOP=1 -c 'CREATE DATABASE roam_backup_b01' >/dev/null
export ROAM_BACKUP_TEST_DSN="host=127.0.0.1 port=15432 user=roam_repro dbname=roam_backup_b01 password=$ROAM_REPRO_DB_PASSWORD sslmode=disable connect_timeout=10"
timeout 480 ./backup-linux-amd64 --db postgres --backup synthetic-backup.encrypted.json --report postgres-report.json --allow-isolated-write
export ROAM_BACKUP_TEST_DSN="roam_repro:$ROAM_REPRO_DB_PASSWORD@tcp(127.0.0.1:13306)/roam_backup_b01?charset=utf8mb4&parseTime=true&timeout=10s&readTimeout=30s&writeTimeout=30s"
timeout 180 ./backup-linux-amd64 --db mysql --backup synthetic-backup.encrypted.json --report mysql-report.json --allow-isolated-write
unset ROAM_BACKUP_TEST_DSN
timeout 180 ./backup-linux-amd64 --db sqlite --sqlite-path restored-linux.sqlite --backup synthetic-backup.encrypted.json --report sqlite-linux-report.json --allow-isolated-write
# This unchanged v0.1.2-behavior diagnostic seeds a DIFFERENT database and proves
# its old sequence defect. --expect-bug is used only here, never by fixed checks.
timeout 180 ./legacy-repro-linux-amd64 --stage verify --db postgres --backup synthetic-backup.encrypted.json --report legacy-report.json --allow-isolated-write --expect-bug
export ROAM_BACKUP_TEST_DSN="host=127.0.0.1 port=15432 user=roam_repro dbname=roam_backup_repro password=$ROAM_REPRO_DB_PASSWORD sslmode=disable connect_timeout=10"
timeout 180 ./backup-linux-amd64 --stage upgrade --db postgres --backup synthetic-backup.encrypted.json --report upgrade-report.json --allow-isolated-write
sha256sum postgres-report.json mysql-report.json sqlite-linux-report.json legacy-report.json upgrade-report.json
