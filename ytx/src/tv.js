// InnerTube through the signed-in TV client, normalised for Promin.
//
// The TV app's responses (`tvBrowseRenderer`, `tileRenderer`, `lockupViewModel`)
// are not modelled by youtubei.js's parser, so we ask for raw JSON and walk
// it ourselves. Every surface reduces to the same shape:
//   { shelves: [{ title, items: [Item], cont }], cont }
//   Item = { kind: 'video'|'channel'|'playlist', id, title, channel: {id,name},
//            duration_sec, duration_text, meta: [..], thumbnail, progress_pct, live }
// Ads (`adSlotRenderer`) are dropped.

import { YTNodes } from 'youtubei.js';
import { text, walk, parseDuration, largestThumb, HttpError } from './util.js';

const PAGES = {
  home: 'FEtopics',
  subscriptions: 'FEsubscriptions',
  history: 'FEhistory',
  playlists: 'FEplaylist_aggregation',
  library: 'FElibrary',
  liked: 'VLLL',
  watch_later: 'VLWL',
};

// ---- item normalisers -------------------------------------------------------

function lines(tile) {
  const out = [];
  for (const l of tile.metadata?.tileMetadataRenderer?.lines || []) {
    const parts = [];
    for (const it of l.lineRenderer?.items || []) {
      const t = text(it.lineItemRenderer?.text);
      if (t && t !== '•') parts.push({ text: t, endpoint: it.lineItemRenderer?.text?.runs?.[0]?.navigationEndpoint });
    }
    out.push(parts);
  }
  return out;
}

function fromTile(t) {
  const type = (t.contentType || '').replace('TILE_CONTENT_TYPE_', '').toLowerCase();
  if (!['video', 'channel', 'playlist'].includes(type)) return null;
  const item = {
    kind: type,
    id: t.contentId,
    title: text(t.metadata?.tileMetadataRenderer?.title),
    channel: null,
    duration_sec: 0,
    duration_text: '',
    meta: [],
    thumbnail: largestThumb(t.header?.tileHeaderRenderer?.thumbnail?.thumbnails),
    progress_pct: 0,
    live: false,
  };
  const ls = lines(t);
  if (ls[0]?.[0]) {
    const ep = ls[0][0].endpoint?.browseEndpoint;
    item.channel = { id: ep?.browseId || null, name: ls[0][0].text };
  }
  for (const l of ls.slice(1)) item.meta.push(l.map((p) => p.text).join(' • '));
  for (const o of t.header?.tileHeaderRenderer?.thumbnailOverlays || []) {
    if (o.thumbnailOverlayTimeStatusRenderer) {
      const s = text(o.thumbnailOverlayTimeStatusRenderer.text);
      if (o.thumbnailOverlayTimeStatusRenderer.style === 'LIVE' || /live/i.test(s)) item.live = true;
      else { item.duration_text = s; item.duration_sec = parseDuration(s); }
    }
    if (o.thumbnailOverlayResumePlaybackRenderer) item.progress_pct = o.thumbnailOverlayResumePlaybackRenderer.percentDurationWatched || 0;
  }
  if (type === 'channel' && !item.title) item.title = text(t.header?.tileHeaderRenderer?.title);
  // Library shelves link to feeds as channel-typed tiles (contentId FEhistory…): not items.
  if (type === 'channel' && /^FE|^VL/.test(item.id || '')) return null;
  return item;
}

// Lockup content types → our three kinds (music videos and shorts are videos,
// albums are playlists).
const LOCKUP_KIND = { VIDEO: 'video', MUSIC: 'video', SHORT: 'video', CHANNEL: 'channel', PLAYLIST: 'playlist', ALBUM: 'playlist', PODCAST: 'playlist' };

function fromLockup(l) {
  const type = LOCKUP_KIND[(l.contentType || '').replace('LOCKUP_CONTENT_TYPE_', '')];
  if (!type) return null;
  const meta = l.metadata?.lockupMetadataViewModel || {};
  const rows = (meta.metadata?.contentMetadataViewModel?.metadataRows || []).map((r) => (r.metadataParts || []).map((p) => text(p.text)).filter(Boolean));
  const thumbVM = l.contentImage?.thumbnailViewModel || l.contentImage?.collectionThumbnailViewModel?.primaryThumbnail?.thumbnailViewModel || {};
  const item = {
    kind: type,
    id: l.contentId,
    title: text(meta.title),
    channel: rows[0]?.[0] ? { id: null, name: rows[0][0] } : null,
    duration_sec: 0,
    duration_text: '',
    meta: rows.slice(1).map((r) => r.join(' • ')),
    thumbnail: largestThumb(thumbVM.image?.sources),
    progress_pct: 0,
    live: false,
  };
  for (const o of thumbVM.overlays || []) {
    for (const b of o.thumbnailBottomOverlayViewModel?.badges || []) {
      const s = b.thumbnailBadgeViewModel?.text || '';
      if (b.thumbnailBadgeViewModel?.badgeStyle === 'THUMBNAIL_OVERLAY_BADGE_STYLE_LIVE') item.live = true;
      else if (/^\d+(:\d+)+$/.test(s)) { item.duration_text = s; item.duration_sec = parseDuration(s); }
    }
    const pb = o.thumbnailOverlayProgressBarViewModel || o.thumbnailBottomOverlayViewModel?.progressBar?.thumbnailOverlayProgressBarViewModel;
    if (pb?.startPercent != null) item.progress_pct = pb.startPercent;
  }
  return item;
}

function itemsOf(list) {
  const out = [];
  for (const it of list || []) {
    if (it.tileRenderer) { const x = fromTile(it.tileRenderer); if (x) out.push(x); }
    else if (it.lockupViewModel) { const x = fromLockup(it.lockupViewModel); if (x) out.push(x); }
    // adSlotRenderer and anything else: skipped
  }
  return out;
}

function shelfTitle(shelf) {
  const h = shelf.headerRenderer?.shelfHeaderRenderer;
  return (text(h?.avatarLockup?.avatarLockupRenderer?.title) || text(h?.title) || '').trim();
}

function contOf(container) {
  return container?.continuations?.[0]?.nextContinuationData?.continuation || null;
}

// Collect shelves (and bare grids) anywhere in a response.
export function normalize(json) {
  const shelves = [];
  let cont = null;
  const gridsInShelves = new Set();
  walk(json, (k, v) => {
    if (k === 'shelfRenderer' && v?.content) {
      const list = v.content.horizontalListRenderer || v.content.gridRenderer || v.content.verticalListRenderer;
      if (!list) return;
      if (v.content.gridRenderer) gridsInShelves.add(v.content.gridRenderer);
      const items = itemsOf(list.items);
      if (items.length) shelves.push({ title: shelfTitle(v), items, cont: contOf(list) });
    } else if (k === 'gridRenderer' && v?.items && !gridsInShelves.has(v)) {
      // A bare grid (history, playlists): one unnamed shelf.
      const items = itemsOf(v.items);
      if (items.length) shelves.push({ title: '', items, cont: contOf(v) });
    } else if (k === 'playlistVideoListRenderer' && v?.contents) {
      // Playlist page: a two-column layout, videos on the right.
      const items = itemsOf(v.contents);
      if (items.length) shelves.push({ title: '', items, cont: contOf(v) });
    } else if (k === 'horizontalListContinuation' || k === 'gridContinuation' || k === 'playlistVideoListContinuation') {
      const items = itemsOf(v.items || v.contents);
      if (items.length) shelves.push({ title: '', items, cont: contOf(v) });
      cont = contOf(v) || cont;
    }
  });
  // A grid inside a shelf is visited twice (shelf first, then the grid key): drop identical copies.
  const seen = new Set();
  const dedup = shelves.filter((s) => { const key = s.items.map((i) => i.id).join(','); if (seen.has(key)) return false; seen.add(key); return true; });
  return { shelves: dedup, cont: cont || (dedup.length === 1 ? dedup[0].cont : null) };
}

// ---- calls ----------------------------------------------------------------

async function raw(yt, endpoint, payload) {
  try {
    const r = await yt.actions.execute(endpoint, { ...payload, parse: false });
    return r.data;
  } catch (e) {
    const msg = String(e?.message || e);
    if (/401|403/.test(msg)) throw new HttpError(401, 'youtube_auth', msg);
    throw new HttpError(502, 'youtube_upstream', msg.slice(0, 200));
  }
}

export async function browse(yt, page, cont) {
  if (cont) return normalize(await raw(yt, '/browse', { continuation: cont }));
  const browseId = PAGES[page] || page; // a channel id (UC…) or playlist (VL…) passes straight through
  if (!/^[A-Za-z0-9_-]{2,64}$/.test(browseId)) throw new HttpError(400, 'bad_page', 'unknown page');
  return normalize(await raw(yt, '/browse', { browseId }));
}

export async function search(yt, q, cont) {
  if (cont) return normalize(await raw(yt, '/search', { continuation: cont }));
  if (!q || q.length > 200) throw new HttpError(400, 'bad_query', 'q required');
  return normalize(await raw(yt, '/search', { query: q }));
}

// Player call with the player's signature timestamp — without it the TV
// client answers "The page needs to be reloaded" and no formats.
export async function player(yt, videoId, reload) {
  const ep = new YTNodes.NavigationEndpoint({ watchEndpoint: { videoId } });
  const args = {
    playbackContext: {
      adPlaybackContext: { pyv: true },
      contentPlaybackContext: { vis: 0, splay: false, lactMilliseconds: '-1', signatureTimestamp: yt.session.player?.signature_timestamp },
    },
    contentCheckOk: true,
    racyCheckOk: true,
    parse: true,
  };
  if (reload) args.playbackContext.reloadPlaybackContext = reload;
  try {
    return await ep.call(yt.actions, args);
  } catch (e) {
    throw new HttpError(502, 'youtube_upstream', String(e?.message || e).slice(0, 200));
  }
}

// Video page: details from /player, related shelves from /next.
export async function video(yt, videoId) {
  if (!/^[A-Za-z0-9_-]{11}$/.test(videoId)) throw new HttpError(400, 'bad_id', 'video id');
  const [pr, next] = await Promise.all([player(yt, videoId), raw(yt, '/next', { videoId })]);
  const d = pr.video_details;
  const ps = pr.playability_status || {};
  const formats = (pr.streaming_data?.adaptive_formats || []).filter((f) => f.has_video && f.quality_label).map((f) => f.quality_label);
  const qualities = [...new Set(formats)].sort((a, b) => parseInt(b) - parseInt(a));
  // The TV player response carries no title; the watch page (/next) does.
  let meta = null;
  walk(next, (k, v) => { if (k === 'videoMetadataRenderer' && !meta) meta = v; });
  const owner = meta?.owner?.videoOwnerRenderer;
  return {
    id: videoId,
    title: text(meta?.title) || d?.title || '',
    channel: { id: owner?.navigationEndpoint?.browseEndpoint?.browseId || d?.channel_id || null, name: text(owner?.title) || d?.author || '' },
    channel_avatar: largestThumb(owner?.thumbnail?.thumbnails),
    duration_sec: d?.duration || 0,
    views_text: text(meta?.viewCount?.videoViewCountRenderer?.viewCount) || '',
    published_text: text(meta?.publishedTimeText) || text(meta?.dateText) || '',
    description: d?.short_description || '',
    is_live: !!d?.is_live,
    thumbnail: largestThumb(d?.thumbnail) || `https://i.ytimg.com/vi/${videoId}/hqdefault.jpg`,
    playable: ps.status === 'OK',
    reason: ps.status === 'OK' ? null : (ps.reason || ps.status || 'unavailable'),
    qualities,
    related: normalize(next).shelves,
  };
}
