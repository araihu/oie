`tiny-video.webm` is an original silent test animation, covered by this
[repository's license](../../../LICENSE). It contains no third-party footage.

The 64×48 VP8/WebM clip was recorded from a browser canvas with
`canvas.captureStream(8)` and `MediaRecorder`, using
`video/webm;codecs=vp8` at 16,000 bits per second. Sixteen frames, drawn
90 milliseconds apart, alternate green and blue backgrounds while moving a
white rectangle. Its duration is about 1.385 seconds and its size is 1,502 bytes.

Browser tests use the committed clip to verify actual decoding and playback.
They do not need FFmpeg, network media, or any external fixture service.

`covered-tone.mp3` is an original one-second 660 Hz tone with a solid-color
16×16 PNG cover, title, artist and custom ID3 text tag. It exercises sending
compatible MP3 originals and preparing M4A with embedded artwork and metadata.

`transparent.webm` is a synthetic 16×16 VP8 clip with a half-transparent red
frame. `covered-video.mp4` combines an H.264 test pattern with the PNG from
`covered-tone.mp3`. The tests cut the MP4 in memory to simulate an incomplete
download. `labeled-covers.mp3` uses the same synthetic tone with two PNGs
tagged as front and back covers. `named-cover.mp3` contains only the back
cover, with a custom description.

`unindexed-opus.weba` contains a two-second synthetic 660 Hz tone encoded
with FFmpeg's libopus at 12 kbps. Its WebM clusters have unknown sizes, omit
cues, and contain audio blocks with the keyframe flag cleared. This exercises
playback of recordings where the browser can read sequentially but cannot
seek back to the beginning after loading metadata.

`tiny-document.txt` and `tiny-document.pdf` are original fixtures covered by
the [repository's license](../../../LICENSE). The UTF-8 text includes literal HTML and
Unicode to check escaping. The PDF is a handcrafted, two-page PDF 1.4 file
with Helvetica titles and subtitles. Its cross-reference offsets and stream
length are computed from the committed bytes. The PDF has no external
resources, scripts or attachments. Neither file contains personal data or
third-party content.

`tiny-hevc.mkv` is an original four-second test pattern and sine wave. Generate it with
`ffmpeg -f lavfi -i testsrc2=size=160x90:rate=12 -f lavfi -i sine=frequency=440:sample_rate=16000 -t 4 -c:v libx265 -x265-params keyint=12 -crf 32 -c:a flac tiny-hevc.mkv`.
