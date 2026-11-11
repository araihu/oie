`tiny-video.webm` is an original silent test animation, covered by this
[repository's license](../../../LICENSE). It contains no third-party footage.

The 64×48 VP8/WebM clip was recorded from a browser canvas with
`canvas.captureStream(8)` and `MediaRecorder`, using
`video/webm;codecs=vp8` at 16,000 bits per second. Sixteen frames, drawn
90 milliseconds apart, alternate green and blue backgrounds while moving a
white rectangle. Its duration is about 1.385 seconds and its size is 1,502 bytes.

Browser tests use the committed clip to verify actual decoding and playback.
They do not need FFmpeg, network media, or any external fixture service.

`tiny-document.txt` and `tiny-document.pdf` are original fixtures covered by
the [repository's license](../../../LICENSE). The UTF-8 text includes literal HTML and
Unicode to check escaping. The PDF is a handcrafted, two-page PDF 1.4 file
with Helvetica titles and subtitles. Its cross-reference offsets and stream
length are computed from the committed bytes. The PDF has no external
resources, scripts or attachments. Neither file contains personal data or
third-party content.
