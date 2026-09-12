<script lang="ts">
  import { linear, niceMax } from "$lib/chart/scale";
  import { formatDur, timeTicks } from "$lib/chart/time";

  export let solves: { x: number; color: string; label: string }[] = [];
  export let height = 190;

  let width = 640;
  const padL = 34;
  const padR = 12;
  const padT = 12;
  const padB = 24;

  $: xMax = solves.length ? Math.max(...solves.map((solve) => solve.x)) : 1;
  $: binCount = Math.max(
    4,
    Math.min(12, Math.ceil(Math.sqrt(Math.max(1, solves.length))) * 2),
  );
  $: binSize = Math.max(1, xMax / binCount);
  $: bins = Array.from({ length: binCount }, (_, index) => {
    const members = solves.filter(
      (solve) =>
        Math.min(binCount - 1, Math.floor(solve.x / binSize)) === index,
    );
    return {
      start: index * binSize,
      end: (index + 1) * binSize,
      count: members.length,
      color: members.at(-1)?.color ?? "#78716c",
      labels: members.map((member) => member.label),
    };
  });
  $: yMax = niceMax(Math.max(1, ...bins.map((bin) => bin.count)));
  $: sx = linear(0, binCount, padL, width - padR);
  $: sy = linear(0, yMax, height - padB, padT);
  $: xTicks = timeTicks(xMax, width < 480 ? 3 : 4);
  $: yTicks = [
    ...new Set([0, 0.5, 1].map((fraction) => Math.round(fraction * yMax))),
  ];
</script>

<div class="w-full" bind:clientWidth={width}>
  <svg
    {width}
    {height}
    viewBox="0 0 {width} {height}"
    class="block w-full"
    role="img"
    aria-label="Solve cadence over time"
  >
    {#each yTicks as tick}
      <line
        x1={padL}
        x2={width - padR}
        y1={sy(tick)}
        y2={sy(tick)}
        class="stroke-stone-800/70"
        stroke-width="1"
      />
      <text
        x={padL - 7}
        y={sy(tick) + 3}
        text-anchor="end"
        class="fill-stone-600 text-[10px] tabular-nums">{tick}</text
      >
    {/each}

    {#each bins as bin, index}
      {@const barWidth = Math.max(2, sx(index + 1) - sx(index) - 3)}
      <rect
        x={sx(index) + 1.5}
        y={sy(bin.count)}
        width={barWidth}
        height={Math.max(0, sy(0) - sy(bin.count))}
        rx="2"
        fill={bin.color}
        fill-opacity={bin.count ? 0.72 : 0}
      >
        <title
          >{formatDur(bin.start)}–{formatDur(bin.end)} · {bin.count} solve{bin.count ===
          1
            ? ""
            : "s"}{bin.labels.length
            ? ` · ${bin.labels.join(", ")}`
            : ""}</title
        >
      </rect>
    {/each}

    {#each xTicks as tick, index}
      <text
        x={linear(0, xMax, padL, width - padR)(tick)}
        y={height - 6}
        text-anchor={index === 0
          ? "start"
          : index === xTicks.length - 1
            ? "end"
            : "middle"}
        class="fill-stone-600 text-[10px] tabular-nums"
      >
        {tick === 0 ? "0" : `+${formatDur(tick)}`}
      </text>
    {/each}
  </svg>
</div>
