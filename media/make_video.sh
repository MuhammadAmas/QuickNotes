#!/bin/bash
# Rebuilds media/QuickNotes-demo.mp4: voiceover clips → video with captions → audio mixed in.
#   media/make_video.sh            (expects media/shots from capture.mjs and capture-frames)
#   VOICE="Ava (Premium)" media/make_video.sh   to use another installed voice
set -euo pipefail
cd "$(dirname "$0")/.."
VOICE="${VOICE:-Samantha}"
RATE="${RATE:-182}"

rm -rf media/voice && mkdir -p media/voice
i=0
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  say -v "$VOICE" -r "$RATE" --file-format=WAVE --data-format=LEI16@44100 -o "$(printf 'media/voice/%02d.wav' $i)" "$line"
  i=$((i + 1))
done < media/voiceover.txt
echo "• $i voiceover clips ($VOICE)"

swiftc -O -o /tmp/qn-video media/video/main.swift desktop/BubbleArt.swift
/tmp/qn-video media/shots media/QuickNotes-demo.mp4 media/voiceover.txt media/voice
