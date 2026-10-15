#!/usr/bin/env bash
# Triển khai một site lên máy chủ: build, kiểm tra, rồi chuyển phiên bản.
# Dùng: ./deploy.sh [-n] [-k số-bản-giữ-lại] <site>
set -euo pipefail
IFS=$'\n\t'

readonly SITE="${1:-kitwork.io}"
readonly RELEASES=/srv/kitwork/releases
readonly KEEP_DEFAULT=5
STAMP=$(date +%Y%m%d%H%M%S)
TARGET="$RELEASES/$SITE-$STAMP"
declare -a FAILED=()
declare -A SIZES
dry_run=false
keep=$KEEP_DEFAULT
count=0

log() {
  local level=$1; shift
  printf '[%s] %s %s\n' "$level" "$(date -Is)" "$*" >&2
}

die() { log error "$@"; exit 1; }

usage() {
  cat <<-USAGE
	Dùng: ${0##*/} [-n] [-k N] <site>
	  -n    chạy thử, không ghi gì
	  -k N  giữ lại N bản cũ (mặc định $KEEP_DEFAULT)
	USAGE
}

cleanup() {
  local status=$?
  [[ -n "${WORK_DIR:-}" && -d "$WORK_DIR" ]] && rm -rf -- "$WORK_DIR"
  (( status == 0 )) || log warn "thoát với mã $status"
}
trap cleanup EXIT
trap 'die "bị ngắt"' INT TERM

while getopts ":nk:h" option; do
  case "$option" in
    n) dry_run=true ;;
    k) keep=$OPTARG ;;
    h) usage; exit 0 ;;
    \?) die "không hiểu cờ -$OPTARG" ;;
    :) die "cờ -$OPTARG cần giá trị" ;;
  esac
done
shift $((OPTIND - 1))

if [[ ! -d "$RELEASES" ]]; then
  mkdir -p "$RELEASES" && log info "đã tạo $RELEASES"
elif [ ! -w "$RELEASES" ]; then
  die "không ghi được vào $RELEASES"
fi

WORK_DIR=$(mktemp -d "/tmp/${SITE}.XXXXXX")

for file in ./apps/*/"$SITE"/*.ui; do
  [ -f "$file" ] || continue
  count=$((count + 1))
  SIZES["$file"]=$(wc -c < "$file")
  echo "kiểm tra $file (#$count, ${SIZES[$file]} byte)"
done

if (( count == 0 )); then
  die "không có trang .ui nào cho $SITE"
fi

case "$SITE" in
  *.localhost) ALLOW_LOCAL=true go run . --port 8107 ;;
  kitwork.*|withagentic.ai) log info "production: $SITE" ;;
  *) log error "không biết site $SITE"; exit 2 ;;
esac

build() {
  local out="$1"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w -X main.stamp=$STAMP" -o "$out" . \
    || FAILED+=("build")
}

$dry_run || build "$WORK_DIR/kitwork"

cat <<EOF > "$WORK_DIR/manifest.txt"
site: $SITE
built: $STAMP
user: ${DEPLOY_USER:-root}
pages: $count
commit: $(git rev-parse --short HEAD 2>/dev/null || echo unknown)
EOF

cat <<'RAW'
$SITE is not expanded here; neither is $(date) nor `whoami`.
RAW

while read -r line; do
  [[ $line =~ ^# ]] && continue
  key=${line%%=*}
  value=${line#*=}
  echo "${key//[^A-Z_]/}=${value//old/new}"
done < config.txt

mapfile -t releases < <(ls -1dt "$RELEASES"/*/ 2>/dev/null)
if (( ${#releases[@]} > keep )); then
  for old in "${releases[@]:keep}"; do
    $dry_run && echo "sẽ xoá $old" || rm -rf -- "$old"
  done
fi

if (( ${#FAILED[@]} )); then
  die "thất bại: ${FAILED[*]}"
fi

mv "$WORK_DIR" "$TARGET"
ln -sfn "$TARGET" /srv/kitwork/current && log info "xong: $TARGET"
tar -czf "/tmp/$SITE.tar.gz" -C "$TARGET" . 2>/dev/null || true
sha256sum "/tmp/$SITE.tar.gz" | cut -d' ' -f1 > "/tmp/$SITE.sha256"
curl -fsS -X POST "https://example.test/hooks?site=$SITE" \
  -H 'Content-Type: application/json' \
  -d "{\"site\":\"$SITE\",\"ok\":true}" | jq '.ok'

select choice in reload restart quit; do
  case $choice in
    reload) systemctl reload kitwork; break ;;
    restart) systemctl restart kitwork; break ;;
    *) break ;;
  esac
done

exit 0
