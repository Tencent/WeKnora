#!/bin/sh
set -eu
export PATH=/opt/mineru-venv/bin:$PATH
export TMPDIR=/state/tmp
export MINERU_API_MAX_CONCURRENT_REQUESTS=1
export MINERU_PROCESSING_WINDOW_SIZE=1
export MINERU_API_ENABLE_VLM_PRELOAD=false
export MINERU_FORMULA_CH_SUPPORT=false
export MINERU_API_TASK_RETENTION_SECONDS=0
# Avoid remote metadata lookup during a measured request after provisioning.
if [ -f /state/home/mineru.json ]; then
  export MINERU_MODEL_SOURCE=local
fi
mkdir -p "$TMPDIR" /state/output
exec mineru-api --host 0.0.0.0 --port 8000
