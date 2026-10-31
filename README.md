# 🎱 BILLIARDS

A neon **8-ball pool** game for the browser: break the rack, claim solids or stripes, run your group and sink the 8 — against a CPU opponent on Easy, Medium or Hard, or a friend on the same screen. Built with **Next.js 16**, **TypeScript** (strict) and **Tailwind CSS v4**, on a hand-written physics engine: balls slide, roll and spin, cuts split at the true contact point, pocket jaws rattle, and a tight rack really breaks. The aiming guide and the CPU both use the same sweep as the simulation, so what you see is what the balls do.

## ✨ Features

| | |
| --- | --- |
| 🎱 Real 8-ball | Break, open table, solids or stripes, ball in hand, and every way to win or lose on the 8 |
| ⚙️ Hand-built physics | Fixed 120 Hz steps with sub-steps and swept collisions, so no ball ever tunnels at any speed |
| 🌀 Spin | Follow and draw from the cue tip; stun shots stop dead, rolling balls follow through |
| 🕳️ Real pockets | Cushions with angled facings and rounded jaws: shots that clip a point rattle out |
| 💥 A break that breaks | Touching balls share the impact through stiff contact springs, so the whole rack spreads |
| 🤖 Three CPU tiers | Ghost-ball aiming and blocked-path checks; Hard test-plays its options in the physics engine for position, safeties and kicks |
| 🎯 Aiming guide | Cue line, ghost ball and the object ball's path, with the pocket ringed when it is straight in |
| 🖐️ Touch and mouse | Pull back to shoot, or drag to aim with a power slider and Shoot button; the table turns upright on phones |
| ⌨️ Keyboard | Aim, fine-aim, spin, charge and shoot, and move a ball in hand without a mouse |
| 🔊 Procedural audio | Cue strike, clicks that follow the impact, cushion thuds and pocket drops — all Web Audio |
| 🏆 Record | Wins and losses against each CPU tier, saved locally and shared across tabs |
| 🌗 Polished | Rolling 3D balls with tumbling numbers, pocket glows, reduced-motion aware, no horizontal scroll |

## 🎮 How to play

1. Pick **vs CPU** (Easy, Medium or Hard) or **2 players** and rack 'em.
2. **Break:** drag the cue ball anywhere behind the line, aim at the rack and hit it hard.
3. The table is **open** after the break. The first ball you pocket legally makes that group — **solids** (1–7) or **stripes** (9–15) — yours. Pocket one of your own and you shoot again.
4. **Fouls** hand your opponent **ball in hand**: pocketing the cue ball, hitting nothing, hitting the wrong ball first (on an open table anything but the 8 is fine), or no ball reaching a cushion or pocket after contact.
5. Clear your group, then sink the **8** to win. Sink it early, or on a foul, and you lose.

**Controls:** mouse — point to aim, press and pull back, release to shoot (or drag the power bar and let go) · touch — drag on the table to aim, set the slider, tap **Shoot** · spin pad — tap high for follow, low for draw.
**Keys:** `←`/`→` aim (`Shift` for fine) · `↑`/`↓` follow/draw · hold `Space` to charge, release to shoot · `Enter` shoots at the meter's power · `WASD` moves a ball in hand · `Esc` puts the cue down.

## 🛠 Tech

- **Next.js 16** (App Router) + **React 19**
- **TypeScript 5**, strict mode
- **Tailwind CSS v4** via `@tailwindcss/postcss`
- **Vitest 3** for the core-logic unit tests
- Pure, framework-free game engine (table geometry, swept-collision physics with spin, aiming sweeps, 8-ball rules, CPU planner) under `src/lib/game`

## Getting started

```bash
npm install
npm run dev      # http://localhost:3000
```

```bash
npm run build    # production build
npm test         # run the unit tests
npm run lint     # lint the project
```
