#!/bin/sh
set -eu
export UV_CACHE_DIR=/state/uv-cache
export UV_LINK_MODE=copy
export TMPDIR=/state/tmp
mkdir -p "$TMPDIR" /state/home /state/models /state/output
if [ ! -x /opt/mineru-venv/bin/python ]; then
  uv venv --python /usr/local/bin/python3 /opt/mineru-venv
fi
requirements=/deployment/requirements.in
if [ -f /deployment/requirements.lock.txt ]; then
  requirements=/deployment/requirements.lock.txt
fi
uv pip install --python /opt/mineru-venv/bin/python --index https://download.pytorch.org/whl/cpu --index-strategy unsafe-best-match -r "$requirements"
uv pip freeze --python /opt/mineru-venv/bin/python > /state/requirements.lock.txt
/opt/mineru-venv/bin/python -c 'import importlib.metadata as m, torch; print({"mineru":m.version("mineru"),"torch":torch.__version__,"cuda":torch.version.cuda})'
/opt/mineru-venv/bin/python -c 'import mineru.backend.pipeline.pipeline_analyze; print("pipeline imports: OK")'
