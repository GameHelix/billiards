/** A point or a direction on the table, in millimetres. */
export interface Vec {
  x: number;
  y: number;
}

/**
 * One ball. `v` is the velocity of its centre; `u` is its *roll velocity* —
 * the speed its spin alone would carry it at if it were rolling cleanly. While
 * the two differ the ball slides and cloth friction pulls them together, which
 * is all that follow, draw and stun are. Ball 0 is the cue ball.
 */
export interface Ball {
  id: number;
  x: number;
  y: number;
  vx: number;
  vy: number;
  ux: number;
  uy: number;
  /** Index of the pocket it dropped into, or -1 while it is on the table. */
  pocket: number;
}

/** A stroke of the cue. */
export interface Shot {
  /** Direction the cue ball is sent, radians (0 = toward the foot rail). */
  angle: number;
  /** 0..1 of full power. */
  power: number;
  /** -1 (full draw) .. 0 (centre ball) .. 1 (full follow). */
  spin: number;
}

export interface PocketedBall {
  id: number;
  pocket: number;
  /** Seconds after the stroke. */
  t: number;
}

/** What the rules need to know about a shot, gathered while it runs. */
export interface ShotEvents {
  /** First object ball the cue ball touched, or null if it touched none. */
  firstContact: number | null;
  /** Cushion contacts by any ball after the first contact. */
  railsAfterContact: number;
  /** Object balls that touched a cushion during the shot. */
  objectBallsToRail: number[];
  /** Balls pocketed, in the order they dropped (the cue ball included). */
  pocketed: PocketedBall[];
  /** The cue ball went into a pocket. */
  scratch: boolean;
}

/** A contact worth a sound or a flash; drained by the view every frame. */
export interface Impact {
  kind: "ball" | "cushion" | "pocket";
  /** Closing speed along the contact normal, mm/s. */
  speed: number;
  x: number;
  y: number;
  /** Ball that dropped, for pocket impacts. */
  id?: number;
  pocket?: number;
}

export type Group = "solids" | "stripes";
export type PlayerIndex = 0 | 1;
export type Difficulty = "easy" | "medium" | "hard";
export type Mode = "cpu" | "local";

/** Where the incoming player may put the cue ball, if anywhere. */
export type BallInHand = "none" | "anywhere" | "kitchen";

export type Foul = "scratch" | "no-contact" | "wrong-ball" | "no-rail";

export type EndReason =
  | "eight-legal" // the winner sank the 8 after clearing their group
  | "eight-early" // the loser sank the 8 before clearing their group
  | "eight-foul"; // the loser sank the 8 on a foul
