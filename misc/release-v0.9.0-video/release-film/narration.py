"""Local English narration, model-timed captions and a reproducible speech bed.

Kokoro af_heart is a stock synthetic voice; no person's voice is cloned.
Only the public, fictional film script is sent to the local model.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import tempfile

import espeakng_loader
import numpy as np
import soundfile as sf

ROOT = Path(__file__).resolve().parent
OUT = ROOT.parent / "out/release-film"
CACHE = OUT / "narration"
CACHE.mkdir(parents=True, exist_ok=True)
os.environ.setdefault("HF_HOME", str(ROOT.parent / "media/hf-cache"))
# espeak's wheel contains a build-machine default. Give it a short directory
# containing espeak-ng-data, rather than the deeply nested workspace path.
data_root = Path(tempfile.gettempdir()) / "weknora-voice-espeak"
data_root.mkdir(exist_ok=True)
data_link = data_root / "espeak-ng-data"
if not data_link.exists():
    data_link.symlink_to(espeakng_loader.get_data_path(), target_is_directory=True)
os.environ["PHONEMIZER_ESPEAK_LIBRARY"] = espeakng_loader.get_library_path()
os.environ["PHONEMIZER_ESPEAK_DATA_PATH"] = str(data_root)
from misaki import espeak
espeak.EspeakWrapper.set_data_path(str(data_root))
from mlx_audio.tts.utils import load_model

MODEL = "mlx-community/Kokoro-82M-bf16"
VOICE = "af_heart"
RATE = 24000
plan = json.loads((ROOT / "narration-plan.json").read_text())
model = load_model(MODEL)
pipeline = model._get_pipeline("a")
bed = np.zeros(165 * RATE, dtype=np.float32)
captions, report = [], []

for index, item in enumerate(plan):
    spoken = item.get("spoken", item["text"])
    available = item["end"] - item["start"] - .08
    speed = .98
    for attempt in range(3):
        signature = hashlib.sha256(f"{MODEL}|{VOICE}|{speed}|{spoken}".encode()).hexdigest()[:16]
        wav, metadata = CACHE / f"{index + 1:02d}-{signature}.wav", CACHE / f"{index + 1:02d}-{signature}.json"
        if wav.exists() and metadata.exists():
            samples, rate = sf.read(wav, dtype="float32")
            assert rate == RATE
            tokens = json.loads(metadata.read_text())
        else:
            parts, tokens, offset = [], [], 0
            for result in pipeline(spoken, voice=VOICE, speed=speed):
                audio = np.array(result.audio).reshape(-1)
                parts.append(audio)
                for token in result.tokens:
                    if token.start_ts is not None and token.end_ts is not None:
                        tokens.append({"text": token.text, "space": token.whitespace,
                                       "start": offset + token.start_ts, "end": offset + token.end_ts})
                offset += len(audio) / RATE
            samples = np.concatenate(parts)
            sf.write(wav, samples, RATE, subtype="FLOAT")
            metadata.write_text(json.dumps(tokens, indent=2))
        seconds = len(samples) / RATE
        if seconds <= available:
            break
        speed = round(speed * seconds / available * 1.015, 3)
        if speed > 1.18:
            raise ValueError(f"Line {index + 1} needs a shorter script: {seconds:.2f}s in {available:.2f}s")
    assert seconds <= available, (index, seconds, available)
    start = round(item["start"] * RATE)
    bed[start:start + len(samples)] += samples

    # Retain model word timing; punctuation groups make calm, readable captions.
    groups, group = [], []
    for token in tokens:
        group.append(token)
        text = "".join(t["text"] + t["space"] for t in group).strip()
        if (len(text) >= 110 and token["space"]) or (token["text"] in ".!?" and len(text) >= 27):
            groups.append(group)
            group = []
    if group:
        if groups and len("".join(t["text"] for t in group)) < 12:
            groups[-1].extend(group)
        else:
            groups.append(group)
    for group in groups:
        text = "".join(t["text"] + t["space"] for t in group).strip()
        text = re.sub(r"Wee\s+Nora", "WeKnora", text)
        text = text.replace("zero point nine", "v0.9.0").replace("M C P", "MCP")
        a = item["start"] + max(0, group[0]["start"] - .055)
        b = min(item["end"], item["start"] + group[-1]["end"] + .13)
        if captions and captions[-1][1] > a:
            captions[-1][1] = round(a, 3)
        captions.append([round(a, 3), round(b, 3), text])
    report.append({**item, "speed": speed, "duration": seconds, "file": wav.name,
                   "peakDbFS": round(float(20 * np.log10(max(np.max(np.abs(samples)), 1e-9))), 2),
                   "words": tokens})
    print(f"{index + 1:02d}/{len(plan)} {seconds:.2f}s / {available:.2f}s · speed {speed} · {item['text']}", flush=True)

sf.write(OUT / "narration-raw.wav", bed, RATE, subtype="FLOAT")
(OUT / "narration-timing.json").write_text(json.dumps({"model": MODEL, "voice": VOICE, "rate": RATE, "duration": 165, "clips": report, "captions": captions}, indent=2))
(ROOT / "narration.js").write_text("// Generated from narration.py and model word durations.\nexport const captions = " + json.dumps(captions, indent=2) + ";\n")
def stamp(t):
    ms = round(t * 1000)
    return f"{ms // 3600000:02}:{ms // 60000 % 60:02}:{ms // 1000 % 60:02},{ms % 1000:03}"
(OUT / "weknora-v0.9.0-en.srt").write_text("\n\n".join(f"{i+1}\n{stamp(a)} --> {stamp(b)}\n{text}" for i, (a, b, text) in enumerate(captions)) + "\n")
print(f"Saved {len(captions)} captions and 165s narration bed.", flush=True)
