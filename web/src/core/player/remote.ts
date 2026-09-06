// Remote-control bridge: the Telegram bot sends `remote` sync events; the
// mounted player registers a handler here so sync.ts stays free of player
// internals. Actions: toggle_play | seek (value = ±seconds) | prev | next |
// mute | sleep (value = minutes). Night mode is global and handled in app.ts.

export type RemoteAction = 'toggle_play' | 'seek' | 'seek_to' | 'prev' | 'next' | 'mute' | 'sleep' | 'night';

let handler: ((action: RemoteAction, value: number) => boolean) | null = null;

export function setRemoteHandler(fn: ((action: RemoteAction, value: number) => boolean) | null): void {
  handler = fn;
}

// True when a mounted player consumed the action.
export function dispatchRemote(action: RemoteAction, value: number): boolean {
  return handler ? handler(action, value) : false;
}
