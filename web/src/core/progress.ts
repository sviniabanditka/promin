// One definition of "watched" and "worth resuming" for the whole client.
// Player (resume prompt), title screen (Continue button), library/home cards
// all used to hard-code 60s / 0.9 separately; the server's continue-watching
// SQL mirrors FINISH_RATIO (store/timecodes_repo.go).

export const FINISH_RATIO = 0.9;
export const RESUME_MIN_S = 60;

export function isFinished(positionSec: number, durationSec: number): boolean {
  return durationSec > 0 && positionSec >= durationSec * FINISH_RATIO;
}

// A saved spot past the intro and before the credits — the only kind the
// player offers to continue from.
export function isResumable(positionSec: number, durationSec: number): boolean {
  return durationSec > 0 && positionSec > RESUME_MIN_S && positionSec < durationSec * FINISH_RATIO;
}
