// The caller owns one mounted input and worker for this attachment.
const isoBrands = new Set(['isom', 'iso2', 'mp41', 'mp42', 'M4A ', 'avc1']);

function selectSD(originalSize, convertedSize, compatible, limit) {
  const fits = size => limit == null || size <= limit;
  if (compatible && fits(originalSize) && convertedSize > originalSize * 0.9) return 'original';
  return fits(convertedSize) ? 'converted' : 'too-large';
}

async function probe(ff, filename, options = []) {
  // Core 0.12.10 can return -1 on success. Validate its fresh JSON report.
  await ff.deleteFile('probe.json').catch(() => {});
  await ff.ffprobe(['-v', 'error', '-protocol_whitelist', 'file', ...options, '-show_streams', '-show_format', '-of', 'json', '-o', 'probe.json', filename]);
  const report = JSON.parse(await ff.readFile('probe.json', 'utf8'));
  if (!report.format || !Array.isArray(report.streams)) throw Error('Cannot identify media tracks');
  return report;
}

function originalFile(file, limit) {
  if (limit != null && file.size > limit) return {route: 'too large'};
  return {route: 'offer file', blob: file};
}

const canCopyAudio = stream => stream.codec_name === 'aac' && stream.profile === 'LC' && stream.channels <= 2;
const trackName = stream => stream.tags?.title || stream.tags?.handler_name;
const dispositions = stream => Object.entries(stream.disposition || {})
  .filter(([, enabled]) => enabled).map(([flag]) => flag).sort().join('+');

function tracksPreserved(input, output) {
  return input.length === output.length && input.every((track, i) => {
    const after = output[i];
    // MP4 marks the only audio or video stream as default. There is no alternate selection.
    const onlyPlayableTrack = track.codec_type !== 'subtitle' &&
      input.filter(s => s.codec_type === track.codec_type).length === 1;
    const flags = stream => dispositions(stream).split('+')
      .filter(flag => flag && !(onlyPlayableTrack && flag === 'default')).join('+');
    return after.codec_type === track.codec_type &&
      (track.tags?.language || 'und') === (after.tags?.language || 'und') &&
      (!trackName(track) || trackName(track) === trackName(after)) &&
      flags(track) === flags(after);
  });
}

async function subtitleText(ff, file, index, copy) {
  const path = 'verify.srt';
  try {
    const code = await ff.exec(['-nostdin', '-y', '-protocol_whitelist', 'file', '-i', file, '-map', '0:' + index,
      '-c:s', copy ? 'copy' : 'srt', '-f', 'srt', path]);
    return code === 0 ? await ff.readFile(path, 'utf8') : null;
  } finally {
    await ff.deleteFile(path).catch(() => {});
  }
}

// Only reads the small, packet-free MP4 produced by our own muxer below.
function childBox(bytes, type) {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  for (let offset = 0; offset + 8 <= bytes.length;) {
    const size = view.getUint32(offset);
    if (size < 8 || size > bytes.length - offset) break;
    if (String.fromCharCode(...bytes.subarray(offset + 4, offset + 8)) === type) {
      return bytes.subarray(offset, offset + size);
    }
    offset += size;
  }
  throw Error('Missing audio metadata');
}

// The iTunes movie metadata carries the cover. Put any remaining descriptive
// tags in a file-level keyed metadata box without rewriting media offsets.
async function additionalMediaMetadata(ff, source, streamType, tags) {
  const path = 'metadata.mp4';
  try {
    const code = await ff.exec(['-nostdin', '-y', '-protocol_whitelist', 'file', '-i', source,
      '-map', '0:' + streamType + ':0', '-frames:' + streamType, '0', '-c', 'copy', '-map_metadata', '-1',
      ...tags.flatMap(([key, value]) => ['-metadata', key + '=' + value]),
      '-movflags', '+use_metadata_tags', path]);
    if (code !== 0) throw Error('Cannot preserve audio metadata');
    const movie = childBox(await ff.readFile(path), 'moov');
    const userData = childBox(movie.subarray(8), 'udta');
    return childBox(userData.subarray(8), 'meta');
  } finally {
    await ff.deleteFile(path).catch(() => {});
  }
}

// Check top-level box lengths without loading media payloads into JS memory.
// A zero-sized final box extends to EOF; packet counts below also cover it.
async function completeMovie(ff, file, source) {
  let windowStart = -1, bytes;
  for (let offset = 0; offset < file.size;) {
    if (!bytes || offset + 16 > windowStart + bytes.byteLength) {
      windowStart = offset;
      bytes = new DataView(await file.slice(offset, offset + 65536).arrayBuffer());
    }
    const at = offset - windowStart;
    if (at + 8 > bytes.byteLength) return false;
    let size = bytes.getUint32(at);
    if (size === 0) break;
    if (size === 1) {
      if (at + 16 > bytes.byteLength) return false;
      size = Number(bytes.getBigUint64(at + 8));
      if (size < 16) return false;
    }
    if (size < 8 || size > file.size - offset) return false;
    offset += size;
  }
  // Ignore edits when comparing against the unedited MP4 sample table.
  // PCM sample counts are not packet counts.
  // The demuxer can emit a *partial* final packet, including in fragmented
  // movies without nb_frames. Observe its diagnostics during the same scan.
  let damaged = false;
  const onLog = ({message}) => {
    if (/\bPacket corrupt \(stream =|\bpartial file\b/i.test(message)) damaged = true;
  };
  ff.on('log', onLog);
  try {
    const {streams} = await probe(ff, source, ['-v', 'warning', '-ignore_editlist', '1', '-count_packets']);
    return !damaged && streams.every(s => !['h264', 'hevc', 'aac', 'mp3', 'av1', 'vp9'].includes(s.codec_name) ||
      s.disposition?.attached_pic || !(Number(s.nb_frames) > (Number(s.nb_read_packets) || 0)));
  } finally {
    ff.off('log', onLog);
  }
}

// Streamed WAV writers can leave the container and data lengths unknown.
async function completeWave(file) {
  const header = new DataView(await file.slice(0, 12).arrayBuffer());
  if (header.byteLength < 12) return false;
  const signature = header.getUint32(0);
  const littleEndian = signature === 0x52494646; // RIFF
  if (!littleEndian && signature !== 0x52494658) return true; // RIFX; RF64 has its own size table.
  const riffSize = header.getUint32(4, littleEndian);
  const unknown = size => size === 0 || size === 0xffffffff;
  if (!unknown(riffSize) && riffSize > file.size - 8) return false;
  const end = unknown(riffSize) ? file.size : riffSize + 8;
  // Skip PCM and ancillary chunk payloads. A data chunk may have a known
  // length even when the container does not.
  for (let offset = 12; offset + 8 <= end;) {
    const chunk = new DataView(await file.slice(offset, offset + 8).arrayBuffer());
    const length = chunk.getUint32(4, littleEndian);
    if (chunk.getUint32(0) === 0x64617461 && unknown(length)) return true; // data
    if (length > end - offset - 8) return false;
    offset += 8 + length + (length % 2);
  }
  return true;
}

function hasTransparency(video) {
  return /^(yuva|gbrap|rgba|argb|abgr|bgra|ya|ayuv|vuya|pal8)/.test(video.pix_fmt) ||
    Object.entries(video.tags || {}).some(([k, v]) => k.toLowerCase() === 'alpha_mode' && String(v) === '1');
}

export async function inspectMedia(ff, file) {
  const dir = '/input';
  await ff.createDir(dir);
  await ff.mount('WORKERFS', {blobs: [{name: 'media', data: file}]}, dir);
  const source = dir + '/media';
  const {streams, format} = await probe(ff, source);
  const artwork = streams.filter(s => s.codec_type === 'video' && s.disposition?.attached_pic);
  const videos = streams.filter(s => s.codec_type === 'video' && !s.disposition?.attached_pic);
  const video = videos[0];
  const audio = streams.filter(s => s.codec_type === 'audio');
  const subtitles = streams.filter(s => s.codec_type === 'subtitle');
  const movie = format.format_name.split(',').includes('mov');
  if (movie && !await completeMovie(ff, file, source)) return {problem:'damaged'};
  if (format.format_name === 'wav' && !await completeWave(file)) return {problem:'damaged'};
  const alpha = video && hasTransparency(video);
  const copyVideo = !video || video.codec_name === 'h264' && video.pix_fmt === 'yuv420p' &&
    ['Constrained Baseline', 'Baseline', 'Main', 'High'].includes(video.profile);
  const supportedArtwork = artwork.every(s => ['png', 'mjpeg'].includes(s.codec_name));
  const supportedTracks = (video || audio.length) && videos.length <= 1 &&
    streams.length === videos.length + audio.length + subtitles.length + artwork.length;
  const mp3 = format.format_name === 'mp3' && !video && !subtitles.length && audio.length === 1 &&
    audio[0].codec_name === 'mp3' && audio[0].channels <= 2;
  const compatible = !!(supportedTracks && !alpha && (mp3 || movie && isoBrands.has(format.tags?.major_brand) &&
    copyVideo && audio.every(canCopyAudio) && subtitles.every(s => s.codec_name === 'mov_text')));
  // An M4A covr entry has image bytes but no per-picture role or description.
  const labeledCovers = artwork.some(({tags = {}}) => {
    if (artwork.length > 1) return tags.title || tags.comment;
    // A single ordinary album cover remains identifiable as the album cover.
    // A custom description or a different APIC role needs the original.
    return (tags.title && tags.title !== 'Album cover') ||
      (tags.comment && !['Cover (front)', 'Other'].includes(tags.comment));
  });
  let problem;
  if (alpha) problem = 'transparent';
  else if (labeledCovers) problem = 'coverLabels';
  else if (!supportedTracks || !supportedArtwork || subtitles.some(s => !['subrip', 'mov_text'].includes(s.codec_name))) problem = 'keepTracks';
  return {problem, compatible, mp3, source, videos, video, audio, subtitles, artwork, tags:format.tags || {}, copyVideo};
}

export async function prepareMedia(ff, file, info, quality, limit) {
  const {source, videos, video, audio, subtitles, artwork, tags, copyVideo, compatible} = info;
  const fits = size => limit == null || size <= limit;
  if (quality === 'OG' || quality === 'HD' && compatible && fits(file.size)) {
    if (!compatible || !fits(file.size)) throw Error('Original unavailable');
    return {blob: file, route: 'original'};
  }
  if (info.problem) return originalFile(file, limit);
  const args = ['-nostdin', '-y', '-xerror', '-noautorotate', '-protocol_whitelist', 'file', '-i', source, '-map_metadata', '0'];
  if (video) {
    args.push('-map', '0:' + video.index);
    if (quality === 'HD' && copyVideo) {
      args.push('-c:v', 'copy');
    } else {
      args.push('-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-preset', 'veryfast',
        '-crf', quality === 'SD' ? '30' : '19', '-threads', '1', '-vsync', '0');
    }
  }
  for (const [index, track] of audio.entries()) {
    const spec = ':a:' + index;
    args.push('-map', '0:' + track.index);
    if (quality === 'HD' && canCopyAudio(track)) {
      args.push('-c' + spec, 'copy');
    } else {
      const channels = track.channels === 1 ? 1 : 2;
      args.push('-c' + spec, 'aac', '-profile' + spec, 'aac_low', '-ac' + spec, String(channels),
        '-b' + spec, String(channels * (quality === 'SD' ? 64 : 128)) + 'k');
    }
  }
  for (const [index, track] of subtitles.entries()) {
    args.push('-map', '0:' + track.index, '-c:s:' + index, track.codec_name === 'mov_text' ? 'copy' : 'mov_text');
  }
  for (const [i, cover] of artwork.entries()) {
    args.push('-map', '0:' + cover.index, '-c:v:' + (videos.length + i), 'copy');
  }
  const tracks = [...videos, ...audio, ...subtitles];
  const mapped = [...tracks, ...artwork];
  for (const [index, track] of mapped.entries()) {
    args.push('-disposition:' + index, dispositions(track) || '0');
    if (trackName(track)) args.push('-metadata:s:' + index, 'handler_name=' + trackName(track));
  }
  const output = video ? 'out.mp4' : 'out.m4a';
  // The M4A muxer writes embedded artwork to covr only in its iTunes metadata mode.
  const flags = artwork.length ? '+faststart' : '+faststart+use_metadata_tags';
  if (await ff.exec([...args, '-movflags', flags, output]) !== 0) return originalFile(file, limit);
  const prepared = await probe(ff, output);
  const playable = prepared.streams.filter(s => !s.disposition?.attached_pic);
  const covers = prepared.streams.filter(s => s.disposition?.attached_pic);
  if (!tracksPreserved(tracks, playable) || covers.length !== artwork.length ||
      covers.some((cover, i) => cover.codec_name !== artwork[i].codec_name ||
        cover.width !== artwork[i].width || cover.height !== artwork[i].height)) return originalFile(file, limit);
  for (const [index, track] of mapped.entries()) {
    if (track.codec_name !== 'subrip') continue;
    const before = await subtitleText(ff, source, track.index, true);
    const after = await subtitleText(ff, output, playable[index].index, false);
    if (before === null || before !== after) return originalFile(file, limit);
  }
  const parts = [await ff.readFile(output)];
  if (artwork.length) {
    const containerTags = ['major_brand', 'minor_version', 'compatible_brands', 'encoder'];
    const extra = Object.entries(tags).filter(([key, value]) =>
      !containerTags.includes(key) && prepared.format.tags?.[key] !== value);
    if (extra.length) parts.push(await additionalMediaMetadata(ff, output, audio.length ? 'a' : 'v', extra));
  }
  const blob = new Blob(parts, {type: video ? 'video/mp4' : 'audio/mp4'});
  let choice = fits(blob.size) ? 'converted' : 'too-large';
  if (quality === 'SD') choice = selectSD(file.size, blob.size, compatible, limit);
  if (choice === 'too-large') return {route:'too large'};
  if (choice === 'original') return {blob: file, route: 'original'};
  return {blob, route: 'converted'};
}
