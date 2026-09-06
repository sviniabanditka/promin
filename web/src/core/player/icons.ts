// Player-panel icons — copied verbatim from Lampa src/templates/player/panel.js
// (voice = "tracks", plus play/pause glyphs). Static markup — no user data.
export const ICO_VOICE =
  '<svg width="24" height="31" viewBox="0 0 24 31" fill="none" xmlns="http://www.w3.org/2000/svg"><rect x="5" width="14" height="23" rx="7" fill="currentColor"/><path d="M3.39272 18.4429C3.08504 17.6737 2.21209 17.2996 1.44291 17.6073C0.673739 17.915 0.299615 18.7879 0.607285 19.5571L3.39272 18.4429ZM23.3927 19.5571C23.7004 18.7879 23.3263 17.915 22.5571 17.6073C21.7879 17.2996 20.915 17.6737 20.6073 18.4429L23.3927 19.5571ZM0.607285 19.5571C2.85606 25.179 7.44515 27.5 12 27.5V24.5C8.55485 24.5 5.14394 22.821 3.39272 18.4429L0.607285 19.5571ZM12 27.5C16.5549 27.5 21.1439 25.179 23.3927 19.5571L20.6073 18.4429C18.8561 22.821 15.4451 24.5 12 24.5V27.5Z" fill="currentColor"/><rect x="10" y="25" width="4" height="6" rx="2" fill="currentColor"/></svg>';
export const ICO_SUBS =
  '<svg width="23" height="25" viewBox="0 0 23 25" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M22.4357 20.0861C20.1515 23.0732 16.5508 25 12.5 25C5.59644 25 0 19.4036 0 12.5C0 5.59644 5.59644 0 12.5 0C16.5508 0 20.1515 1.9268 22.4357 4.9139L18.8439 7.84254C17.2872 6.09824 15.0219 5 12.5 5C7.80558 5 5 7.80558 5 12.5C5 17.1944 7.80558 20 12.5 20C15.0219 20 17.2872 18.9018 18.8439 17.1575L22.4357 20.0861Z" fill="currentColor"/></svg>';
export const ICO_PP_PLAY =
  '<svg width="22" height="25" viewBox="-3 0 25 25" fill="currentColor" xmlns="http://www.w3.org/2000/svg"><path d="M21 10.768c1.333 0.77 1.333 2.694 0 3.464l-17.25 9.96c-1.333 0.77 -3 -0.193 -3 -1.733V2.541C0.75 1 2.417 0.039 3.75 0.809z"/></svg>';
export const ICO_PP_PAUSE =
  '<svg width="19" height="25" viewBox="0 0 19 25" fill="none" xmlns="http://www.w3.org/2000/svg"><rect width="6" height="25" rx="2" fill="currentColor"/><rect x="13" width="6" height="25" rx="2" fill="currentColor"/></svg>';
// Prev / next episode (single triangle + stop bar) — Lampa panel.js __prev/__next.
export const ICO_PREV =
  '<svg width="23" height="24" viewBox="0 0 23 24" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M2.75 13.7698C1.41666 13 1.41667 11.0755 2.75 10.3057L20 0.34638C21.3333 -0.42342 23 0.538831 23 2.07843L23 21.997C23 23.5366 21.3333 24.4989 20 23.7291L2.75 13.7698Z" fill="currentColor"/><rect x="6" y="24" width="6" height="24" rx="2" transform="rotate(180 6 24)" fill="currentColor"/></svg>';
export const ICO_NEXT =
  '<svg width="23" height="24" viewBox="0 0 23 24" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M20.25 10.2302C21.5833 11 21.5833 12.9245 20.25 13.6943L3 23.6536C1.66666 24.4234 -6.72981e-08 23.4612 0 21.9216L8.70669e-07 2.00298C9.37967e-07 0.463381 1.66667 -0.498867 3 0.270933L20.25 10.2302Z" fill="currentColor"/><rect x="17" width="6" height="24" rx="2" fill="currentColor"/></svg>';
// Skip to start / end (double triangle + bar) — Lampa __tstart/__tend.
export const ICO_TSTART =
  '<svg width="35" height="24" viewBox="0 0 35 24" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M14.75 10.2302C13.4167 11 13.4167 12.9245 14.75 13.6943L32 23.6536C33.3333 24.4234 35 23.4612 35 21.9216L35 2.00298C35 0.463381 33.3333 -0.498867 32 0.270933L14.75 10.2302Z" fill="currentColor"/><path d="M1.75 10.2302C0.416665 11 0.416667 12.9245 1.75 13.6943L19 23.6536C20.3333 24.4234 22 23.4612 22 21.9216L22 2.00298C22 0.463381 20.3333 -0.498867 19 0.270933L1.75 10.2302Z" fill="currentColor"/><rect width="6" height="24" rx="2" transform="matrix(-1 0 0 1 6 0)" fill="currentColor"/></svg>';
export const ICO_TEND =
  '<svg width="35" height="24" viewBox="0 0 35 24" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M20.25 10.2302C21.5833 11 21.5833 12.9245 20.25 13.6943L3 23.6536C1.66666 24.4234 -6.72981e-08 23.4612 0 21.9216L8.70669e-07 2.00298C9.37967e-07 0.463381 1.66667 -0.498867 3 0.270933L20.25 10.2302Z" fill="currentColor"/><path d="M33.25 10.2302C34.5833 11 34.5833 12.9245 33.25 13.6943L16 23.6536C14.6667 24.4234 13 23.4612 13 21.9216L13 2.00298C13 0.463381 14.6667 -0.498867 16 0.270933L33.25 10.2302Z" fill="currentColor"/><rect x="29" width="6" height="24" rx="2" fill="currentColor"/></svg>';
// Rewind back / forward (double triangle, no bar) — Lampa __rprev/__rnext.
export const ICO_RPREV =
  '<svg width="35" height="25" viewBox="0 0 35 25" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M14 10.7679C12.6667 11.5377 12.6667 13.4622 14 14.232L31.25 24.1913C32.5833 24.9611 34.25 23.9989 34.25 22.4593L34.25 2.5407C34.25 1.0011 32.5833 0.0388526 31.25 0.808653L14 10.7679Z" fill="currentColor"/><path d="M0.999998 10.7679C-0.333335 11.5377 -0.333333 13.4622 1 14.232L18.25 24.1913C19.5833 24.9611 21.25 23.9989 21.25 22.4593L21.25 2.5407C21.25 1.0011 19.5833 0.0388526 18.25 0.808653L0.999998 10.7679Z" fill="currentColor"/></svg>';
export const ICO_RNEXT =
  '<svg width="35" height="25" viewBox="0 0 35 25" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M20.25 10.7679C21.5833 11.5377 21.5833 13.4622 20.25 14.232L3 24.1913C1.66666 24.9611 -6.72981e-08 23.9989 0 22.4593L8.70669e-07 2.5407C9.37967e-07 1.0011 1.66667 0.0388526 3 0.808653L20.25 10.7679Z" fill="currentColor"/><path d="M33.25 10.7679C34.5833 11.5377 34.5833 13.4622 33.25 14.232L16 24.1913C14.6667 24.9611 13 23.9989 13 22.4593L13 2.5407C13 1.0011 14.6667 0.0388526 16 0.808653L33.25 10.7679Z" fill="currentColor"/></svg>';
// Picture-in-picture — Lampa __pip.
export const ICO_PIP =
  '<svg width="25" height="23" viewBox="0 0 25 23" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M4 2H21C22.1046 2 23 2.89543 23 4V11H25V4C25 1.79086 23.2091 0 21 0H4C1.79086 0 0 1.79086 0 4V19C0 21.2091 1.79086 23 4 23H9V21H4C2.89543 21 2 20.1046 2 19V4C2 2.89543 2.89543 2 4 2Z" fill="currentColor"/><path d="M11.0988 12.2064C11.7657 12.3811 12.3811 11.7657 12.2064 11.0988L11.2241 7.34718C11.0494 6.68023 10.2157 6.46192 9.72343 6.95423L6.95422 9.72344C6.46192 10.2157 6.68022 11.0494 7.34717 11.2241L11.0988 12.2064Z" fill="currentColor"/><path d="M7.53735 9.45591C8.06025 9.97881 8.91363 9.97322 9.44343 9.44342C9.97322 8.91362 9.97882 8.06024 9.45592 7.53734L6.93114 5.01257C6.40824 4.48967 5.55486 4.49526 5.02506 5.02506C4.49527 5.55485 4.48967 6.40823 5.01257 6.93113L7.53735 9.45591Z" fill="currentColor"/><rect x="12" y="14" width="13" height="9" rx="2" fill="currentColor"/></svg>';
// Volume — Lampa __volume.
export const ICO_VOLUME =
  '<svg width="360" height="480" viewBox="0 0 360 480" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M145 366.736V123.602L290.343 30.3787C309.644 17.9988 335 31.8587 335 54.789V425.73C335 448.023 310.894 461.98 291.561 450.88L145 366.736Z" stroke="currentColor" stroke-width="45"/><path d="M60 134H136V357H60C40.67 357 25 341.33 25 322V169C25 149.67 40.67 134 60 134Z" stroke="currentColor" stroke-width="45"/></svg>';
// Muted — volume glyph with a slash across it.
export const ICO_VOLUME_MUTE =
  '<svg width="360" height="480" viewBox="0 0 360 480" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M145 366.736V123.602L290.343 30.3787C309.644 17.9988 335 31.8587 335 54.789V425.73C335 448.023 310.894 461.98 291.561 450.88L145 366.736Z" stroke="currentColor" stroke-width="45"/><path d="M60 134H136V357H60C40.67 357 25 341.33 25 322V169C25 149.67 40.67 134 60 134Z" stroke="currentColor" stroke-width="45"/><path d="M20 20L340 460" stroke="currentColor" stroke-width="45" stroke-linecap="round"/></svg>';
// Settings (gear) — Lampa __settings.
// Back arrow (info bar) — chevron left.
export const ICO_BACK =
  '<svg width="26" height="26" viewBox="0 0 26 26" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M16 4L7 13L16 22" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/></svg>';

// Single left/right press step (seconds), matches Lampa's default player_rewind.
// Episode list (three lines with bullets).
export const ICO_EPISODES =
  '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg"><circle cx="4" cy="6" r="2" fill="currentColor"/><circle cx="4" cy="12" r="2" fill="currentColor"/><circle cx="4" cy="18" r="2" fill="currentColor"/><rect x="8" y="4.5" width="14" height="3" rx="1.5" fill="currentColor"/><rect x="8" y="10.5" width="14" height="3" rx="1.5" fill="currentColor"/><rect x="8" y="16.5" width="14" height="3" rx="1.5" fill="currentColor"/></svg>';
// Framing toggle (fit ↔ fill): a frame with arrows to the corners.
export const ICO_ASPECT =
  '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg"><rect x="2" y="4" width="20" height="16" rx="2" stroke="currentColor" stroke-width="2"/><path d="M6 8h4M6 8v4M18 16h-4M18 16v-4" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>';
// "More" (three dots) — opens the secondary sheet: PiP, sound, framing, subtitle size.
export const ICO_MORE =
  '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg"><circle cx="5" cy="12" r="2.4" fill="currentColor"/><circle cx="12" cy="12" r="2.4" fill="currentColor"/><circle cx="19" cy="12" r="2.4" fill="currentColor"/></svg>';
