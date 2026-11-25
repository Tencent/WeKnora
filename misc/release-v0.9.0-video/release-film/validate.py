"""Validate the encoded release film and extract changed scenes for review."""
import hashlib
import json
from pathlib import Path
import struct
import subprocess

OUT = Path(__file__).resolve().parent.parent / "out/release-film"
movie = OUT / "weknora-v0.9.0-narrated-en.mp4"
ffmpeg, ffprobe = "/opt/homebrew/bin/ffmpeg", "/opt/homebrew/bin/ffprobe"
probe = json.loads(subprocess.check_output([ffprobe, "-v", "error", "-show_streams", "-show_format", "-of", "json", str(movie)]))
video = next(x for x in probe['streams'] if x['codec_type'] == 'video')
audio = next(x for x in probe['streams'] if x['codec_type'] == 'audio')
assert (video['codec_name'], video['width'], video['height'], video['r_frame_rate'], int(video['nb_frames'])) == ('h264', 1920, 1080, '60/1', 9900)
assert (audio['codec_name'], audio['sample_rate'], audio['channels']) == ('aac', '48000', 2)
assert abs(float(probe['format']['duration']) - 165) < .03
decode = subprocess.run([ffmpeg, '-v', 'error', '-i', str(movie), '-f', 'null', '-'], capture_output=True, text=True, check=True)
assert not decode.stderr.strip(), decode.stderr
data = movie.read_bytes()
atoms, offset = [], 0
while offset < len(data):
    size, name = struct.unpack('>I4s', data[offset:offset+8])
    if size == 1:
        size = struct.unpack('>Q', data[offset+8:offset+16])[0]
    atoms.append(name.decode())
    offset += size or len(data)-offset
assert atoms.index('moov') < atoms.index('mdat')
measure = subprocess.run([ffmpeg, '-hide_banner', '-i', str(movie), '-vn', '-af',
    'loudnorm=I=-18:TP=-2:LRA=7:print_format=json', '-f', 'null', '-'], capture_output=True, text=True, check=True).stderr
levels = json.JSONDecoder().raw_decode(measure[measure.rfind('{'):])[0]
assert float(levels['input_tp']) < -1
stills = OUT / 'encoded-narrated'
stills.mkdir(exist_ok=True)
times = [0, 1.5, 7, 19, 26.6, 33, 38.7, 43, 54.95, 55, 62, 70, 76, 83, 94.2, 96.6, 97.5, 98.2, 100, 109.95, 110, 117.8, 119.3, 120.9, 123.4, 125, 133.4, 139, 145, 148, 155, 157, 159, 164]
for t in times:
    subprocess.run([ffmpeg, '-v', 'error', '-y', '-ss', str(t), '-i', str(movie), '-frames:v', '1', str(stills / f'{t:06.2f}.png')], check=True)
report = {'file': movie.name, 'duration': float(probe['format']['duration']), 'width': 1920, 'height': 1080,
    'fps': 60, 'frames': int(video['nb_frames']), 'videoCodec': 'h264', 'audioCodec': 'aac', 'sampleRate': 48000,
    'channels': 2, 'fullDecodePassed': True, 'fastStart': True, 'encodedStills': len(times),
    'integratedLufs': float(levels['input_i']), 'truePeakDbTP': float(levels['input_tp']),
    'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest()}
(OUT / 'validation.json').write_text(json.dumps(report, indent=2))
print(json.dumps(report, indent=2))
