#!/bin/bash
set -e

# Docker overmounts paths like /proc/acpi, /proc/scsi etc. which breaks
# nsjail's /proc bind-mount. Drop them first (proven in tested/entrypoint.sh).
echo "[entrypoint] Cleaning /proc overmounts (if any)..."
awk '$2 ~ /^\/proc\// {print $2}' /proc/mounts | sort -r | while read -r mnt; do
    umount "$mnt" 2>/dev/null || true
done

echo "[entrypoint] Starting executor-worker..."
exec executor-worker
