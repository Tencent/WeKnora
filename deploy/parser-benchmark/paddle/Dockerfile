FROM python:3.11-slim-bookworm@sha256:528257d48c1da0dcecc2e725d1ae34498d60c965f1241e39cd6a85a8859bdf84
ENV PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    OMP_NUM_THREADS=6 \
    MKL_NUM_THREADS=6 \
    HOME=/runtime/home \
    HF_HOME=/runtime/huggingface \
    XDG_CACHE_HOME=/runtime/cache \
    PADDLE_PDX_DISABLE_MODEL_SOURCE_CHECK=True
RUN apt-get update && apt-get install -y --no-install-recommends libgomp1 libgl1 libglib2.0-0 && rm -rf /var/lib/apt/lists/*
COPY requirements.txt /opt/paddle/requirements.txt
RUN python -m pip install --no-cache-dir -r /opt/paddle/requirements.txt && python -m pip check
COPY requirements.lock /opt/paddle/requirements.lock
RUN python -m pip install --no-cache-dir --no-deps -r /opt/paddle/requirements.lock && python -m pip check
COPY pipeline.yaml prepare.py smoke.py models.lock.json /opt/paddle/
WORKDIR /opt/paddle
EXPOSE 8080
CMD ["paddlex", "--serve", "--pipeline", "/opt/paddle/pipeline.yaml", "--device", "cpu", "--host", "0.0.0.0", "--port", "8080"]
