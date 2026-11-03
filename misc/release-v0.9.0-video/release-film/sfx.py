"""Typing only: a crisp, short keyboard press for each visible input character.

The exact character timestamps come from film.js, quantized to the export frame
rate. This stem has no music, ambience or voice; mix.py combines it with the
separately generated narration and ducks it while the narrator speaks.
"""
import json
import wave
from pathlib import Path
import numpy as np

OUT = Path(__file__).resolve().parent.parent / "out" / "release-film"
timing = json.loads((OUT / "timing.json").read_text())
SR = 48000
rng = np.random.default_rng(9090)
audio = np.zeros((round(timing["duration"] * SR), 2), dtype=np.float64)
coverage = np.zeros(len(audio), dtype=bool)
for event in timing["keyEvents"]:
    length = .038
    t = np.arange(round(SR * length)) / SR
    # A short contact transient: retain the clear upper-mid click while removing
    # the low keycap thump that made the accepted version sound muffled.
    raw = rng.normal(size=len(t))
    kernel = np.array([1., 3., 4., 3., 1.]) / 12
    bright = np.convolve(raw, kernel, mode="same")
    body = np.convolve(bright, np.ones(19) / 19, mode="same")
    contact = bright - body * .94
    attack = np.minimum(t / .00018, 1)
    press = contact * np.exp(-t / .0037) * attack
    velocity = rng.uniform(.80, 1.0) * (.68 if event["char"] == " " else 1)
    for frequency, gain, decay in [(760, .009, .004), (2350, .06, .003), (3900, .035, .0018)]:
        press += gain * np.sin(2 * np.pi * frequency * rng.uniform(.95, 1.05) * t) * np.exp(-t / decay) * attack
    release = np.maximum(t - .016, 0)
    press += contact * .12 * np.exp(-release / .003) * (t >= .016) * np.minimum(release / .0003, 1)
    press *= np.minimum((length - t) / .005, 1).clip(0, 1) * velocity
    pan = rng.uniform(.46, .54)
    i = round(event["t"] * SR)
    j = min(len(audio), i + len(press))
    audio[i:j, 0] += press[:j-i] * np.sqrt(1-pan)
    audio[i:j, 1] += press[:j-i] * np.sqrt(pan)
    coverage[i:j] = True

# Conservative peak level; do not loudness-normalize sparse typing into loud noise.
audio *= 10 ** (-13.5 / 20) / max(np.abs(audio).max(), 1e-9)
assert np.max(np.abs(audio[~coverage])) == 0
with wave.open(str(OUT / "typing.wav"), "wb") as wav:
    wav.setnchannels(2)
    wav.setsampwidth(2)
    wav.setframerate(SR)
    wav.writeframes((audio * 32767).astype('<i2').tobytes())
(OUT / "audio-checks.json").write_text(json.dumps({
    "duration": timing["duration"], "sampleRate": SR,
    "characterEvents": len(timing["keyEvents"]),
    "peakDbFS": float(20*np.log10(np.abs(audio).max())),
    "nonTypingSamplesAreSilent": True, "music": False, "narration": False,
    "sound": "crisp upper-mid contact, reduced bass thump, 38 ms tail",
}, indent=2))
print(f"Wrote typing.wav: {len(timing['keyEvents'])} synchronized key presses; no music.")
