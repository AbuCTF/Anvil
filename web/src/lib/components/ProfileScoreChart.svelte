<script lang="ts">
  import { stepPath } from "$lib/chart/path";
  import { linear, niceMax } from "$lib/chart/scale";
  import { formatDur, timeTicks } from "$lib/chart/time";

  // points are cumulative: x = seconds since first solve, y = running total.
  export let points: { x: number; y: number; color: string; label: string }[] =
    [];
  export let height = 260;

  let width = 640;
  const padL = 44;
  const padR = 14;
  const padT = 14;
  const padB = 24;

  $: xMax = points.length ? Math.max(...points.map((p) => p.x)) : 1;
  $: yMax = niceMax(points.length ? Math.max(...points.map((p) => p.y)) : 1);
  $: sx = linear(0, xMax === 0 ? 1 : xMax, padL, width - padR);
  $: sy = linear(0, yMax, height - padB, padT);
  $: screen = points.map((p) => ({ ...p, cx: sx(p.x), cy: sy(p.y) }));
  $: line = stepPath(screen.map((p) => ({ x: p.cx, y: p.cy })));
  $: area = screen.length
    ? `${stepPath(screen.map((p) => ({ x: p.cx, y: p.cy })))} L ${sx(xMax)} ${sy(0)} L ${padL} ${sy(0)} Z`
    : "";
  $: yTicks = [
    ...new Set([0, 0.25, 0.5, 0.75, 1].map((f) => Math.round(f * yMax))),
  ];
  $: xTicks = timeTicks(xMax, width < 480 ? 3 : 4);

  let hoverIndex: number | null = null;
  $: hoverPoint = hoverIndex == null ? null : (screen[hoverIndex] ?? null);
  $: tipLeft =
    hoverPoint == null
      ? 0
      : Math.min(Math.max(0, hoverPoint.cx + 12), Math.max(0, width - 196));

  function onMove(event: PointerEvent) {
    if (screen.length === 0) return;
    const rect = (event.currentTarget as SVGElement).getBoundingClientRect();
    const cursorX =
      ((event.clientX - rect.left) / Math.max(1, rect.width)) * width;
    let low = 0;
    let high = screen.length - 1;
    while (low < high) {
      const middle = Math.floor((low + high) / 2);
      if (screen[middle].cx < cursorX) low = middle + 1;
      else high = middle;
    }
    const previous = Math.max(0, low - 1);
    hoverIndex =
      Math.abs(screen[previous].cx - cursorX) <=
      Math.abs(screen[low].cx - cursorX)
        ? previous
        : low;
  }
</script>

<div class="relative w-full" bind:clientWidth={width}>
  <svg
    {width}
    {height}
    viewBox="0 0 {width} {height}"
    class="block w-full"
    role="img"
    aria-label="Cumulative solve points over time"
    on:pointermove={onMove}
    on:pointerleave={() => (hoverIndex = null)}
    on:pointercancel={() => (hoverIndex = null)}
  >
    <defs>
      <linearGradient id="scoreFill" x1="0" x2="0" y1="0" y2="1">
        <stop offset="0%" stop-color="#f59e0b" stop-opacity="0.22" />
        <stop offset="100%" stop-color="#f59e0b" stop-opacity="0" />
      </linearGradient>
    </defs>

    {#each yTicks as t}
      <line
        x1={padL}
        x2={width - padR}
        y1={sy(t)}
        y2={sy(t)}
        class="stroke-stone-800"
        stroke-width="1"
      />
      <text
        x={padL - 8}
        y={sy(t) + 3}
        text-anchor="end"
        class="fill-stone-500 text-[10px] tabular-nums">{t}</text
      >
    {/each}
    {#each xTicks as t, index}
      <text
        x={sx(t)}
        y={height - 6}
        text-anchor={index === 0
          ? "start"
          : index === xTicks.length - 1
            ? "end"
            : "middle"}
        class="fill-stone-600 text-[10px] tabular-nums"
      >
        {t === 0 ? "0" : `+${formatDur(t)}`}
      </text>
    {/each}

    {#if area}
      <path d={area} fill="url(#scoreFill)" stroke="none" />
      <path
        d={line}
        fill="none"
        stroke="#f59e0b"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    {/if}

    {#if hoverPoint}
      <line
        x1={hoverPoint.cx}
        x2={hoverPoint.cx}
        y1={padT}
        y2={height - padB}
        class="stroke-stone-600"
        stroke-width="1"
        stroke-dasharray="3 3"
      />
    {/if}

    {#each screen as p, i}
      <circle
        cx={p.cx}
        cy={p.cy}
        r={hoverIndex === i ? 5 : 3.5}
        fill={p.color}
        stroke="#0c0a09"
        stroke-width="1.5"
      >
        <title>{p.label} · +{formatDur(p.x)} · {p.y} pts</title>
      </circle>
    {/each}
  </svg>

  {#if hoverPoint}
    <div
      class="pointer-events-none absolute top-2 z-10 w-[184px] rounded-md border border-stone-700 bg-stone-950/95 px-2.5 py-2 text-xs shadow-lg"
      style="left: {tipLeft}px;"
    >
      <div class="flex items-center gap-1.5 leading-none">
        <span
          class="w-2 h-2 rounded-full shrink-0"
          style="background: {hoverPoint.color};"
        ></span>
        <span class="optical-label text-stone-200 truncate"
          >{hoverPoint.label}</span
        >
      </div>
      <div
        class="mt-1.5 flex items-center justify-between text-stone-500 tabular-nums"
      >
        <span>+{formatDur(hoverPoint.x)}</span>
        <span class="text-stone-300">{hoverPoint.y.toLocaleString()} pts</span>
      </div>
    </div>
  {/if}
</div>
