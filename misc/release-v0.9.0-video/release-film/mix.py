"""Normalize the English narration and duck synchronized typing underneath it."""
import json
from pathlib import Path
import subprocess

OUT = Path(__file__).resolve().parent.parent / "out/release-film"
FFMPEG = "/opt/homebrew/bin/ffmpeg"
voice = OUT / "narration-raw.wav"
analysis = subprocess.run([FFMPEG, "-hide_banner", "-i", str(voice), "-af",
    "loudnorm=I=-18:TP=-2:LRA=7:print_format=json", "-f", "null", "-"],
    capture_output=True, text=True, check=True).stderr
measure = json.JSONDecoder().raw_decode(analysis[analysis.rfind("{"):])[0]
normalizer = ("loudnorm=I=-18:TP=-2:LRA=7:linear=true"
    f":measured_I={measure['input_i']}:measured_TP={measure['input_tp']}"
    f":measured_LRA={measure['input_lra']}:measured_thresh={measure['input_thresh']}"
    f":offset={measure['target_offset']}")
subprocess.run([FFMPEG, "-v", "error", "-y", "-i", str(voice), "-af", normalizer,
    "-ar", "48000", "-ac", "2", str(OUT / "narration.wav")], check=True)
graph = ("[0:a]asplit=2[voice][detector];[1:a]volume=0.55[keys];"
    "[keys][detector]sidechaincompress=threshold=0.012:ratio=8:attack=5:release=110:makeup=1[quietkeys];"
    "[voice][quietkeys]amix=inputs=2:duration=longest:normalize=0,alimiter=limit=0.94:level=false:latency=true[mix]")
subprocess.run([FFMPEG, "-v", "error", "-y", "-i", str(OUT / "narration.wav"),
    "-i", str(OUT / "typing.wav"), "-filter_complex", graph, "-map", "[mix]",
    "-ar", "48000", "-ac", "2", "-t", "165", str(OUT / "soundtrack.wav")], check=True)
analysis = subprocess.run([FFMPEG, "-hide_banner", "-i", str(OUT / "soundtrack.wav"),
    "-af", "loudnorm=I=-18:TP=-2:LRA=7:print_format=json", "-f", "null", "-"],
    capture_output=True, text=True, check=True).stderr
result = json.JSONDecoder().raw_decode(analysis[analysis.rfind("{"):])[0]
timing = json.loads((OUT / "narration-timing.json").read_text())
assert all(c['start'] + c['duration'] <= c['end'] for c in timing['clips'])
assert float(result['input_tp']) < -1
(OUT / "mix-checks.json").write_text(json.dumps({
    "duration": 165, "music": False, "narration": True, "stockSyntheticVoice": timing['voice'],
    "speechClips": len(timing['clips']), "captionCues": len(timing['captions']),
    "narrationWithinSceneSlots": True, "typingGain": .55,
    "ducking": "8:1, 5ms attack, 110ms release, keyed by narration",
    "integratedLufs": float(result['input_i']), "truePeakDbTP": float(result['input_tp']),
    "loudnessRangeLu": float(result['input_lra']), "sampleRate": 48000, "channels": 2,
}, indent=2))
print(json.dumps(result, indent=2))
