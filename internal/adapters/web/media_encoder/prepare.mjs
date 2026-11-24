// The caller owns one mounted input and worker for this attachment.
const isoBrands = new Set(['isom', 'iso2', 'mp41', 'mp42', 'M4A ', 'avc1']);

function selectSD(originalSize, convertedSize, compatible, limit) {
  const fits = size => limit == null || size <= limit;
  if (compatible && fits(originalSize) && convertedSize > originalSize * 0.9) return 'original';
  return fits(convertedSize) ? 'converted' : 'too-large';
}

async function probe(ff, filename) {
  // Core 0.12.10 can return -1 on success. Validate its fresh JSON report.
  await ff.deleteFile('probe.json').catch(() => {});
  await ff.ffprobe(['-v', 'error', '-protocol_whitelist', 'file', '-show_streams', '-show_format', '-of', 'json', '-o', 'probe.json', filename]);
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
async function additionalAudioMetadata(ff, source, tags) {
  const path = 'metadata.mp4';
  try {
    const code = await ff.exec(['-nostdin', '-y', '-protocol_whitelist', 'file', '-i', source,
      '-map', '0:a:0', '-frames:a', '0', '-c:a', 'aac', '-map_metadata', '-1',
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
  if ((!video && !audio.length) || videos.length > 1 ||
      (artwork.length && (video || artwork.some(s => !['png', 'mjpeg'].includes(s.codec_name)))) ||
      /^(yuva|gbrap|rgba|argb|abgr|bgra|ya)/.test(video?.pix_fmt) || video?.tags?.alpha_mode === '1' ||
      streams.length !== videos.length + audio.length + subtitles.length + artwork.length ||
      subtitles.some(s => !['mov_text', 'subrip'].includes(s.codec_name))) {
    return {preservable: false};
  }
  const copyVideo = !video || video.codec_name === 'h264' && video.pix_fmt === 'yuv420p' &&
    ['Constrained Baseline', 'Baseline', 'Main', 'High'].includes(video.profile);
  const namedForPlayback = video ? /\.(mp4|m4v)$/i.test(file.name) : /\.(m4a|mp4)$/i.test(file.name);
  const mp3 = format.format_name === 'mp3' && /\.mp3$/i.test(file.name) && !video &&
    !subtitles.length && audio.length === 1 && audio[0].codec_name === 'mp3' && audio[0].channels <= 2;
  const compatible = mp3 || namedForPlayback && copyVideo && audio.every(canCopyAudio) &&
    subtitles.every(s => s.codec_name === 'mov_text') && isoBrands.has(format.tags?.major_brand);
  return {preservable: true, source, videos, video, audio, subtitles, artwork, tags:format.tags || {}, copyVideo, compatible};
}

export async function prepareMedia(ff, file, info, quality, limit) {
  if (!info.preservable) return originalFile(file, limit);
  const {source, videos, video, audio, subtitles, artwork, tags, copyVideo, compatible} = info;
  const fits = size => limit == null || size <= limit;
  if (quality === 'OG' || quality === 'HD' && compatible && fits(file.size)) {
    if (!compatible || !fits(file.size)) throw Error('Original unavailable');
    return {blob: file, route: 'original'};
  }
  const args = ['-nostdin', '-y', '-noautorotate', '-protocol_whitelist', 'file', '-i', source, '-map_metadata', '0'];
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
  for (const cover of artwork) args.push('-map', '0:' + cover.index);
  if (artwork.length) args.push('-c:v', 'copy');
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
    const after = await subtitleText(ff, output, prepared.streams[index].index, false);
    if (before === null || before !== after) return originalFile(file, limit);
  }
  const parts = [await ff.readFile(output)];
  if (artwork.length) {
    const containerTags = ['major_brand', 'minor_version', 'compatible_brands', 'encoder'];
    const extra = Object.entries(tags).filter(([key, value]) =>
      !containerTags.includes(key) && prepared.format.tags?.[key] !== value);
    if (extra.length) parts.push(await additionalAudioMetadata(ff, source, extra));
  }
  const blob = new Blob(parts, {type: video ? 'video/mp4' : 'audio/mp4'});
  let choice = fits(blob.size) ? 'converted' : 'too-large';
  if (quality === 'SD') choice = selectSD(file.size, blob.size, compatible, limit);
  if (choice === 'too-large') return {route:'too large'};
  if (choice === 'original') return {blob: file, route: 'original'};
  return {blob, route: 'converted'};
}
