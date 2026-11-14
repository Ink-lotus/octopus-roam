#!/usr/bin/env bash
# B04: isolated C01-C08, 3x3 restore, and complete B01-B03 regression.
set -euo pipefail
umask 077
task_root=${1:?absolute isolated task directory required}
case "$task_root" in
  /data/home/roam-backup-b04-20[0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9]) ;;
  *) echo 'Unexpected B04 task directory.' >&2; exit 1 ;;
esac
cd "$task_root"
source /data/home/roam-backup-integrity-tools-20261112-01/env.sh
test "$(go env GOVERSION)" = go1.27.3
test "$(node --version)" = v24.18.0
test "$(pnpm --version)" = 11.15.1
test "$(python3 --version)" = "Python 3.14.6"
sha256sum --check inputs.sha256
pg_name=roam-backup-b04-pg18
mysql_name=roam-backup-b04-mysql84
task_label=backup-b04
pg_image=postgres@sha256:e923a22ac074261dd73970c57edf659475bf0497cb9456f17d98ed7cdd3ca913
mysql_image=mysql@sha256:72c368d898861f3b3a07af0e6d690400f44f048799ed46822783d9224cc2c86d
for name in "$pg_name" "$mysql_name"; do
  if docker container inspect "$name" >/dev/null 2>&1; then echo 'Existing B04 container; refusing adoption.' >&2; exit 1; fi
done
for path in postgres-integrity-report.json mysql-integrity-report.json sqlite-integrity-report.json postgres-b01-report.json mysql-b01-report.json sqlite-b01-report.json postgres-statistics-report.json mysql-statistics-report.json sqlite-statistics-report.json postgres-upgrade-report.json mysql-upgrade-report.json sqlite-upgrade-report.json legacy-statistics.sqlite; do
  test ! -e "$path"
done
docker image inspect --format '{{json .RepoDigests}}' "$pg_image" "$mysql_image"
chmod 700 backup-linux-amd64 op-linux.test v012-linux legacy-repro-linux-amd64
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
  unset ROAM_REPRO_DB_PASSWORD ROAM_BACKUP_TEST_DSN POSTGRES_PASSWORD MYSQL_PASSWORD MYSQL_ROOT_PASSWORD ROAM_STATS_TEST_DSN
  local remaining
  remaining=$(docker ps -a --filter "label=roam.task=$task_label" --format '{{.Names}}') || result=1
  if [ -n "$remaining" ]; then result=1; fi
  printf 'cleanup_exit=%s remaining_owned_containers=%s\n' "$result" "${remaining:-none}"
  exit "$result"
}
trap cleanup EXIT
export ROAM_REPRO_DB_PASSWORD
ROAM_REPRO_DB_PASSWORD=$(python3 -c 'import secrets; print(secrets.token_hex(32))')
export POSTGRES_PASSWORD="$ROAM_REPRO_DB_PASSWORD" MYSQL_PASSWORD="$ROAM_REPRO_DB_PASSWORD"
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
for database in roam_backup_b03 roam_backup_b01 roam_backup_b02 roam_backup_b02_upgrade roam_backup_b02_clock; do
  docker exec "$pg_name" psql -U roam_repro -d roam_backup_repro -v ON_ERROR_STOP=1 -c "CREATE DATABASE $database" >/dev/null
done
docker exec "$mysql_name" sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql --user=root --execute="CREATE DATABASE roam_backup_b03; CREATE DATABASE roam_backup_b02; CREATE DATABASE roam_backup_b02_upgrade; CREATE DATABASE roam_backup_b02_clock; GRANT ALL ON roam_backup_b03.* TO '\''roam_repro'\''@'\''%'\''; GRANT ALL ON roam_backup_b02.* TO '\''roam_repro'\''@'\''%'\''; GRANT ALL ON roam_backup_b02_upgrade.* TO '\''roam_repro'\''@'\''%'\''; GRANT ALL ON roam_backup_b02_clock.* TO '\''roam_repro'\''@'\''%'\'';"'
pg_dsn() { printf 'host=127.0.0.1 port=15432 user=roam_repro dbname=%s password=%s sslmode=disable connect_timeout=10' "$1" "$ROAM_REPRO_DB_PASSWORD"; }
mysql_dsn() { printf 'roam_repro:%s@tcp(127.0.0.1:13306)/%s?charset=utf8mb4&parseTime=true&timeout=10s&readTimeout=30s&writeTimeout=30s' "$ROAM_REPRO_DB_PASSWORD" "$1"; }
export ROAM_BACKUP_TEST_DSN
new_database() {
  local database=$1
  case "$database" in roam_backup_b04_*) ;; *) exit 2 ;; esac
  [[ "$database" =~ ^[a-z0-9_]+$ ]] || exit 2
  docker exec "$pg_name" psql -U roam_repro -d roam_backup_repro -v ON_ERROR_STOP=1 -c "CREATE DATABASE $database" >/dev/null
  docker exec --env B04_DATABASE="$database" "$mysql_name" sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql --user=root --execute="CREATE DATABASE $B04_DATABASE; GRANT ALL ON $B04_DATABASE.* TO '\''roam_repro'\''@'\''%'\'';"'
}
for case_number in 01 02 03 04 05 06 07 08; do
  database="roam_backup_b04_c$case_number"
  new_database "$database"
  for dialect in postgres mysql sqlite; do
    if [ "$dialect" = postgres ]; then ROAM_BACKUP_TEST_DSN=$(pg_dsn "$database"); fi
    if [ "$dialect" = mysql ]; then ROAM_BACKUP_TEST_DSN=$(mysql_dsn "$database"); fi
    timeout 180 ./backup-linux-amd64 --stage matrix --scenario "C$case_number" --db "$dialect" \
      --sqlite-path "matrix-C$case_number.sqlite" --backup synthetic-backup.encrypted.json \
      --report "$dialect-C$case_number-report.json" --allow-isolated-write
  done
done
for source in sqlite mysql postgres; do
  database="roam_backup_b04_cross_${source}_seed"
  new_database "$database"
  if [ "$source" = postgres ]; then ROAM_BACKUP_TEST_DSN=$(pg_dsn "$database"); fi
  if [ "$source" = mysql ]; then ROAM_BACKUP_TEST_DSN=$(mysql_dsn "$database"); fi
  timeout 180 ./backup-linux-amd64 --stage cross-export --db "$source" --sqlite-path "cross-$source-seed.sqlite" \
    --backup synthetic-backup.encrypted.json --cross-backup "$source-export.encrypted.json" \
    --report "$source-cross-export-report.json" --allow-isolated-write
done
for source in sqlite mysql postgres; do
  for target in sqlite mysql postgres; do
    database="roam_backup_b04_cross_${source}_$target"
    new_database "$database"
    if [ "$target" = postgres ]; then ROAM_BACKUP_TEST_DSN=$(pg_dsn "$database"); fi
    if [ "$target" = mysql ]; then ROAM_BACKUP_TEST_DSN=$(mysql_dsn "$database"); fi
    timeout 180 ./backup-linux-amd64 --stage cross-import --db "$target" --sqlite-path "cross-$source-$target.sqlite" \
      --backup synthetic-backup.encrypted.json --cross-backup "$source-export.encrypted.json" \
      --report "$source-to-$target-cross-report.json" --allow-isolated-write
  done
done
for dialect in postgres mysql sqlite; do
  if [ "$dialect" = postgres ]; then ROAM_BACKUP_TEST_DSN=$(pg_dsn roam_backup_b03); fi
  if [ "$dialect" = mysql ]; then ROAM_BACKUP_TEST_DSN=$(mysql_dsn roam_backup_b03); fi
  timeout 300 ./backup-linux-amd64 --stage integrity --db "$dialect" --sqlite-path restored-integrity.sqlite --backup synthetic-backup.encrypted.json --report "$dialect-integrity-report.json" --allow-isolated-write
done
ROAM_BACKUP_TEST_DSN=$(pg_dsn roam_backup_b01)
timeout 480 ./backup-linux-amd64 --db postgres --backup synthetic-backup.encrypted.json --report postgres-b01-report.json --allow-isolated-write
ROAM_BACKUP_TEST_DSN=$(mysql_dsn roam_backup_b01)
timeout 180 ./backup-linux-amd64 --db mysql --backup synthetic-backup.encrypted.json --report mysql-b01-report.json --allow-isolated-write
timeout 180 ./backup-linux-amd64 --db sqlite --sqlite-path restored-b01.sqlite --backup synthetic-backup.encrypted.json --report sqlite-b01-report.json --allow-isolated-write
# The diagnostic is unchanged v0.1.2 behavior, separate from all fixed assertions.
timeout 180 ./legacy-repro-linux-amd64 --stage verify --db postgres --backup synthetic-backup.encrypted.json --report legacy-b01-report.json --allow-isolated-write --expect-bug
ROAM_BACKUP_TEST_DSN=$(pg_dsn roam_backup_repro)
timeout 180 ./backup-linux-amd64 --stage upgrade --db postgres --backup synthetic-backup.encrypted.json --report upgrade-b01-report.json --allow-isolated-write
for dialect in postgres mysql sqlite; do
  if [ "$dialect" = postgres ]; then ROAM_BACKUP_TEST_DSN=$(pg_dsn roam_backup_b02); fi
  if [ "$dialect" = mysql ]; then ROAM_BACKUP_TEST_DSN=$(mysql_dsn roam_backup_b02); fi
  timeout 180 ./backup-linux-amd64 --stage statistics --db "$dialect" --sqlite-path restored-statistics.sqlite --backup synthetic-backup.encrypted.json --report "$dialect-statistics-report.json" --allow-isolated-write
  export ROAM_LEGACY_DB_DIALECT="$dialect"
  if [ "$dialect" = postgres ]; then ROAM_BACKUP_TEST_DSN=$(pg_dsn roam_backup_b02_upgrade); fi
  if [ "$dialect" = mysql ]; then ROAM_BACKUP_TEST_DSN=$(mysql_dsn roam_backup_b02_upgrade); fi
  if [ "$dialect" = sqlite ]; then ROAM_BACKUP_TEST_DSN="$task_root/legacy-statistics.sqlite"; fi
  timeout 100 python3 seed-v012.py "$task_root/v012-linux" "$task_root/v012-$dialect"
  timeout 180 ./backup-linux-amd64 --stage statistics-prepare --db "$dialect" --sqlite-path legacy-statistics.sqlite --backup synthetic-backup.encrypted.json --report "$dialect-prepare-report.json" --allow-isolated-write
  timeout 240 ./backup-linux-amd64 --stage statistics-upgrade --db "$dialect" --sqlite-path legacy-statistics.sqlite --backup synthetic-backup.encrypted.json --report "$dialect-upgrade-report.json" --allow-isolated-write
  export ROAM_STATS_TEST_ALLOW_ISOLATED_WRITE=1
  if [ "$dialect" = sqlite ]; then
    unset ROAM_STATS_TEST_DIALECT ROAM_STATS_TEST_DSN
  else
    export ROAM_STATS_TEST_DIALECT="$dialect" ROAM_STATS_TEST_DSN
    if [ "$dialect" = postgres ]; then ROAM_STATS_TEST_DSN=$(pg_dsn roam_backup_b02_clock); else ROAM_STATS_TEST_DSN=$(mysql_dsn roam_backup_b02_clock); fi
  fi
  timeout 180 ./op-linux.test -test.run '^TestStatsBusinessKeysAndMidnight$' -test.v -test.timeout=150s > "$dialect-clock.log" 2>&1
  cat "$dialect-clock.log"
done
sha256sum ./*-report.json ./*-clock.log
