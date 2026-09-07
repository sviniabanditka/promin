// Remote-control bridge: the Telegram bot sends `remote` sync events; the
// mounted player registers a handler here so sync.ts stays free of player
// internals. Actions: toggle_play | seek (value = ±seconds) | prev | next |
// mute | sleep (value = minutes) | set_voice (str = voice id) | set_subtitle
// (str = subtitle id | "off") | volume (value 0..100). Night mode is global
// and handled in app.ts.

export type RemoteAction = 'toggle_play' | 'seek' | 'seek_to' | 'prev' | 'next' | 'mute' | 'sleep' | 'night' | 'set_voice' | 'set_subtitle' | 'volume';

let handler: ((action: RemoteAction, value: number, str: string) => boolean) | null = null;

export function setRemoteHandler(fn: ((action: RemoteAction, value: number, str: string) => boolean) | null): void {
  handler = fn;
}

// True when a mounted player consumed the action.
export function dispatchRemote(action: RemoteAction, value: number, str?: string): boolean {
  return handler ? handler(action, value, str || '') : false;
}
