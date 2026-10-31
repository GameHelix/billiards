import type { Difficulty } from "./types";

/*
 * Units: millimetres and seconds. The playing surface is a 7-ft bar table
 * measured nose-to-nose of the cushions, so the cloth spans x ∈ [0, L] and
 * y ∈ [0, W]. The head (break) end is x = 0 and the rack sits toward x = L.
 */

/** Playing-surface length, cushion nose to cushion nose. */
export const TABLE_L = 2000;
/** Playing-surface width; exactly half the length, as on a real table. */
export const TABLE_W = 1000;
/** Ball radius (a 57 mm ball, the regulation 2¼"). */
export const BALL_R = 28.5;
export const BALL_D = BALL_R * 2;

/** Distance between the two jaw points of a corner pocket (≈ 2.15 balls). */
export const CORNER_MOUTH = 108;
/** Distance between the two jaw points of a side pocket (≈ 2.3 balls). */
export const SIDE_MOUTH = 118;
/** Depth of the cushion rubber from its nose to the rail, used for the facings. */
export const CUSHION_DEPTH = 40;
/** Radius of the rounded nose where a cushion turns into its pocket facing. */
export const JAW_RADIUS = 7;
/**
 * Facing angles, measured between the cushion nose and the facing. Above 135°
 * a corner pocket's facings converge, so a ball has to be aimed into the
 * throat rather than merely near it; the side facings are almost square.
 */
export const CORNER_FACING_DEG = 138;
export const SIDE_FACING_DEG = 102;

/** The head string: the cue ball goes behind it after a break scratch. */
export const HEAD_STRING_X = TABLE_L / 4;
export const HEAD_SPOT = { x: TABLE_L / 4, y: TABLE_W / 2 } as const;
/** Apex of the rack, and where a pocketed 8 is re-spotted after the break. */
export const FOOT_SPOT = { x: (TABLE_L * 3) / 4, y: TABLE_W / 2 } as const;
/** Where the cue ball starts for the break. */
export const BREAK_SPOT = { x: TABLE_L / 4 - 40, y: TABLE_W / 2 } as const;
/**
 * Racked balls touch: a tight rack is what lets the break spread through the
 * whole triangle (see the touching-group solver in physics.ts).
 */
export const RACK_GAP = 0;

/* ------------------------------------------------------------- physics */

/** Fixed simulation step. Every shot, live or imagined by the CPU, uses it. */
export const TICK = 1 / 120;
/**
 * Sub-steps keep each one's travel under this distance. Collisions are swept
 * continuously anyway, so this mostly keeps the friction integration smooth.
 */
export const MAX_SUBSTEP_TRAVEL = BALL_R * 0.5;
export const MAX_SUBSTEPS = 24;
/** Cue-ball speed at full power, mm/s. */
export const MAX_SPEED = 6200;
/** Softest stroke the controls allow, as a fraction of full power. */
export const MIN_POWER = 0.03;
/** Ball-on-ball coefficient of restitution (phenolic resin is very lively). */
export const BALL_RESTITUTION = 0.95;
/** Cushion coefficient of restitution. */
export const CUSHION_RESTITUTION = 0.8;
/** Share of the along-cushion speed a ball keeps through a cushion contact. */
export const CUSHION_GRIP = 0.97;
/** Sliding friction μ·g on cloth, mm/s². Drives stun, draw and follow. */
export const SLIDE_DECEL = 1960;
/** Rolling resistance, mm/s². */
export const ROLL_DECEL = 260;
/** Extra speed-proportional drag, 1/s, so long rolls settle in sensible time. */
export const DRAG = 0.24;
/** Below this speed a rolling ball is snapped to rest. */
export const STOP_SPEED = 7;
/** Most off-centre tip contact, as a fraction of the radius (beyond it: miscue). */
export const MAX_TIP_OFFSET = 0.5;
/** Hard ceiling on one shot's simulated duration, seconds. */
export const MAX_SHOT_TIME = 45;

/* ---------------------------------------------------------------- game */

export const DIFFICULTIES: readonly Difficulty[] = ["easy", "medium", "hard"];

export const DIFFICULTY_LABEL: Record<Difficulty, string> = {
  easy: "Easy",
  medium: "Medium",
  hard: "Hard",
};

/** localStorage key for the persisted win/loss record. */
export const STATS_KEY = "billiards.stats.v1";
/** localStorage key for the last menu choices. */
export const PREFS_KEY = "billiards.prefs.v1";

/** Ball colours, index = ball number; stripes 9–15 reuse the solids' hues. */
export const BALL_COLORS: readonly string[] = [
  "#f8fafc", // cue
  "#facc15", // 1 yellow
  "#3b82f6", // 2 blue
  "#ef4444", // 3 red
  "#a855f7", // 4 purple
  "#f97316", // 5 orange
  "#22c55e", // 6 green
  "#be123c", // 7 maroon
  "#1e1b2e", // 8 black
  "#facc15",
  "#3b82f6",
  "#ef4444",
  "#a855f7",
  "#f97316",
  "#22c55e",
  "#be123c",
];
