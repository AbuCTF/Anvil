// Package game implements the Attack-Defense + King-of-the-Hill engine: the tick
// controller, checker dispatch, flag lifecycle, SLA tracking, KotH hill control
// with round resets, scoring, and the signed-webhook standings emitter.
//
// It owns the game_* tables (migration 010) and posts signed standings to the
// scoreboard. ATTACK, DEFENSE, SLA, and KOTH are additive, so disabling the KotH
// layer degrades to plain Attack-Defense.
package game
