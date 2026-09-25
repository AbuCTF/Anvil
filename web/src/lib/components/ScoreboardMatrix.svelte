<script lang="ts">
  import { onMount } from "svelte";
  import OpticalIcon from "$lib/components/OpticalIcon.svelte";
  import { categoryColor, rankAccent, teamColor } from "$lib/rank";

  interface MatrixChallenge {
    slug: string;
    name: string;
    category: string;
    category_color: string;
    points: number;
  }

  interface MatrixCell {
    s: number;
    b: number;
  }

  interface MatrixRow {
    rank: number;
    user_id: string;
    username: string;
    name: string;
    total: number;
    delta: number;
    cells: MatrixCell[];
  }

  export let challenges: MatrixChallenge[] = [];
  export let rows: MatrixRow[] = [];
  export let totalPlayers = rows.length;
  export let search = "";
  export let category = "";
  export let sort: "rank" | "name" = "rank";

  const ROWS_PER_PAGE = 50;
  let matrixPage = 1;
  let scrollViewport: HTMLDivElement;
  let viewportWidth = 0;

  onMount(() => {
    const updateViewportWidth = () => {
      if (!scrollViewport) return;
      viewportWidth = scrollViewport.clientWidth;
    };
    const observer = new ResizeObserver(updateViewportWidth);
    updateViewportWidth();
    if (scrollViewport) observer.observe(scrollViewport);
    return () => observer.disconnect();
  });

  $: indexedChallenges = challenges.map((challenge, index) => ({
    ...challenge,
    index,
  }));
  $: visibleChallenges = category
    ? indexedChallenges.filter((challenge) => challenge.category === category)
    : indexedChallenges;
  $: groups = (() => {
    const result: { name: string; color: string; count: number }[] = [];
    for (const challenge of visibleChallenges) {
      const previous = result.at(-1);
      if (previous?.name === challenge.category) previous.count += 1;
      else
        result.push({
          name: challenge.category,
          color: categoryColor(challenge.category),
          count: 1,
        });
    }
    return result;
  })();
  $: visibleRows = (() => {
    const query = search.trim().toLowerCase();
    const filtered = query
      ? rows.filter(
          (row) =>
            row.name.toLowerCase().includes(query) ||
            row.username.toLowerCase().includes(query),
        )
      : rows;
    return sort === "name"
      ? [...filtered].sort((a, b) => a.name.localeCompare(b.name))
      : filtered;
  })();
  $: totalPages = Math.max(1, Math.ceil(visibleRows.length / ROWS_PER_PAGE));
  $: safePage = Math.min(matrixPage, totalPages);
  $: playerColumnWidth = Math.min(
    352,
    Math.max(256, viewportWidth > 0 ? viewportWidth * 0.24 : 256),
  );
  $: challengeColumnWidth = visibleChallenges.length
    ? Math.max(
        44,
        (Math.max(viewportWidth, playerColumnWidth) - playerColumnWidth) /
          visibleChallenges.length,
      )
    : 44;
  $: matrixWidth =
    playerColumnWidth + challengeColumnWidth * visibleChallenges.length;
  $: pageRows = visibleRows.slice(
    (safePage - 1) * ROWS_PER_PAGE,
    safePage * ROWS_PER_PAGE,
  );
  $: playerSummary = search.trim()
    ? `${visibleRows.length} match${visibleRows.length === 1 ? "" : "es"} within ${
        totalPlayers > rows.length
          ? `top ${rows.length} of ${totalPlayers.toLocaleString()}`
          : rows.length
      } players`
    : `${totalPlayers > rows.length ? `Top ${rows.length} of ${totalPlayers.toLocaleString()}` : visibleRows.length} players`;

  function shortName(name: string) {
    const words = name.trim().split(/\s+/);
    if (words.length > 1) {
      const suffix = words.at(-1);
      if (suffix && /^\d+$/.test(suffix)) {
        return `${words[0].slice(0, 2)}${suffix}`;
      }
      return words
        .map((word) => word[0])
        .join("")
        .slice(0, 3);
    }
    return name.slice(0, 3);
  }

  function cellTitle(
    row: MatrixRow,
    challenge: MatrixChallenge,
    cell: MatrixCell,
  ) {
    if (!cell?.s) return `${row.name} has not solved ${challenge.name}`;
    const blood = cell.b > 0 && cell.b <= 3 ? ` · blood #${cell.b}` : "";
    return `${row.name} solved ${challenge.name}${blood}`;
  }

  function bloodClass(rank: number) {
    if (rank === 1) return "border-blood/50 bg-blood/20 text-blood";
    if (rank === 2) return "border-stone-400/60 bg-stone-400/15 text-stone-200";
    return "border-amber-700/60 bg-amber-700/20 text-amber-500";
  }
</script>

{#if visibleChallenges.length === 0}
  <div class="px-4 py-10 text-center text-sm text-stone-500">
    No challenges in this category.
  </div>
{:else}
  <div
    bind:this={scrollViewport}
    class="matrix-scrollbar overflow-x-auto lg:max-h-[70vh] lg:overflow-auto"
    style={`--matrix-player-width: ${playerColumnWidth}px; --matrix-challenge-width: ${challengeColumnWidth}px;`}
  >
    <table
      class="table-fixed border-separate border-spacing-0 text-sm"
      style={`width: ${matrixWidth}px;`}
    >
      <colgroup>
        <col class="matrix-player-column" />
        {#each visibleChallenges as challenge (challenge.slug)}
          <col class="matrix-challenge-column" data-challenge={challenge.slug} />
        {/each}
      </colgroup>
      <thead>
        <tr>
          <th
            rowspan="2"
            scope="col"
            class="matrix-player-column metadata-label sticky left-0 top-0 z-40 border-b border-r border-stone-800 bg-stone-900 px-4 py-2.5 text-left text-stone-500"
          >
            Player
          </th>
          {#each groups as group}
            <th
              colspan={group.count}
              scope="colgroup"
              class="metadata-label sticky top-0 z-30 h-9 border-b border-r border-stone-800/70 bg-stone-900 px-2 py-2 text-left"
              style="color: {group.color};"
            >
              {group.name}
            </th>
          {/each}
        </tr>
        <tr>
          {#each visibleChallenges as challenge}
            <th
              scope="col"
              class="matrix-challenge-column sticky top-9 z-30 h-12 border-b border-r border-stone-800/70 bg-stone-900 px-1 text-center font-normal"
              title="{challenge.name} · {challenge.points} pts"
            >
              <a
                href="/challenges/{challenge.slug}"
                class="metadata-label inline-flex h-8 w-8 items-center justify-center rounded text-stone-500 hover:bg-stone-800/60 hover:text-stone-200"
                aria-label="{challenge.name}, {challenge.points} points"
              >
                {shortName(challenge.name)}
              </a>
            </th>
          {/each}
        </tr>
      </thead>
      <tbody>
        {#each pageRows as row (row.user_id)}
          <tr class="group">
            <th
              scope="row"
              class="matrix-player-column sticky left-0 z-20 border-b border-r border-stone-800/70 bg-stone-950 px-4 py-2.5 text-left font-normal group-hover:bg-stone-900"
            >
              <div class="flex items-center gap-2.5 leading-none">
                <span
                  class="w-[6ch] shrink-0 whitespace-nowrap text-right font-semibold tabular-nums {rankAccent(
                    row.rank,
                  )}">#{row.rank}</span
                >
                <span
                  class="h-2 w-2 shrink-0 rounded-full"
                  style="background: {teamColor(row.user_id)};"
                ></span>
                <a
                  href="/profile/{row.username}"
                  class="min-w-0 flex-1 truncate text-stone-200 hover:text-amber-400"
                  title={row.name}
                  >{row.name}</a
                >
                <span class="shrink-0 text-xs tabular-nums text-stone-500"
                  >{row.total.toLocaleString()}</span
                >
                {#if row.delta !== 0}
                  <span
                    class="{row.delta > 0
                      ? 'text-up'
                      : 'text-down'} inline-flex shrink-0 items-center text-[0.65rem] tabular-nums"
                  >
                    <OpticalIcon
                      icon={row.delta > 0 ? "mdi:menu-up" : "mdi:menu-down"}
                      size={12}
                      box={12}
                    />
                    <span class="optical-label">{Math.abs(row.delta)}</span>
                  </span>
                {/if}
              </div>
            </th>
            {#each visibleChallenges as challenge}
              {@const cell = row.cells[challenge.index] ?? { s: 0, b: 0 }}
              <td
                class="matrix-challenge-column h-11 border-b border-r border-stone-800/60 bg-stone-950/40 p-1 text-center group-hover:bg-stone-900/50"
              >
                <span
                  class="mx-auto flex h-7 w-7 items-center justify-center rounded-full border leading-none {cell.b >
                    0 && cell.b <= 3
                    ? bloodClass(cell.b)
                    : cell.s
                      ? 'border-up/60 bg-up/[0.04] text-up'
                      : 'border-dashed border-stone-800 text-stone-700'}"
                  title={cellTitle(row, challenge, cell)}
                  role="img"
                  aria-label={cellTitle(row, challenge, cell)}
                >
                  {#if cell.b > 0 && cell.b <= 3}
                    <span
                      class="blood-label text-[0.62rem] font-semibold tabular-nums"
                      >{cell.b}</span
                    >
                  {:else if cell.s}
                    <span class="h-2 w-2 rounded-full bg-current"></span>
                  {/if}
                </span>
              </td>
            {/each}
          </tr>
        {/each}
        {#if visibleRows.length === 0}
          <tr>
            <td
              colspan={visibleChallenges.length + 1}
              class="px-4 py-10 text-center text-stone-500"
            >
              No players match “{search}”.
            </td>
          </tr>
        {/if}
      </tbody>
    </table>
  </div>

  <div
    class="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-stone-800 px-4 py-3 text-xs text-stone-500"
  >
    <span class="metadata-label text-stone-600">
      {playerSummary} · {visibleChallenges.length} challenges
    </span>
    {#if totalPages > 1}
      <div
        class="inline-flex items-center overflow-hidden rounded border border-stone-800"
        aria-label="Matrix pages"
      >
        <button
          type="button"
          disabled={safePage === 1}
          on:click={() => (matrixPage = safePage - 1)}
          class="inline-flex h-6 w-7 items-center justify-center text-stone-500 hover:text-stone-200 disabled:cursor-not-allowed disabled:opacity-30"
          aria-label="Previous matrix page"
        >
          <OpticalIcon icon="mdi:chevron-left" size={12} box={12} />
        </button>
        <span
          class="metadata-label border-x border-stone-800 px-2 py-1 text-stone-500"
          >{safePage}/{totalPages}</span
        >
        <button
          type="button"
          disabled={safePage === totalPages}
          on:click={() => (matrixPage = safePage + 1)}
          class="inline-flex h-6 w-7 items-center justify-center text-stone-500 hover:text-stone-200 disabled:cursor-not-allowed disabled:opacity-30"
          aria-label="Next matrix page"
        >
          <OpticalIcon icon="mdi:chevron-right" size={12} box={12} />
        </button>
      </div>
    {/if}
    <span class="ml-auto inline-flex items-center gap-1.5 leading-none">
      <span class="h-3 w-3 rounded-full border border-up/60 bg-up/[0.04]"
      ></span>
      <span class="optical-label">Solved</span>
    </span>
    {#each [1, 2, 3] as rank}
      <span class="inline-flex items-center gap-1.5 leading-none">
        <span
          class="relative top-[0.5px] inline-flex h-4 w-4 items-center justify-center rounded-full border text-[0.58rem] font-semibold tabular-nums {bloodClass(
            rank,
          )}"
        >
          <span class="blood-label">{rank}</span>
        </span>
        <span class="optical-label"
          >{rank === 1 ? "First" : rank === 2 ? "Second" : "Third"} blood</span
        >
      </span>
    {/each}
  </div>
{/if}

<style>
  .matrix-player-column {
    width: var(--matrix-player-width);
    min-width: var(--matrix-player-width);
    max-width: var(--matrix-player-width);
  }

  .matrix-challenge-column {
    width: var(--matrix-challenge-width);
    min-width: var(--matrix-challenge-width);
  }

  .matrix-scrollbar {
    overscroll-behavior: contain;
    scrollbar-gutter: stable;
    scrollbar-color: rgb(var(--st-700)) rgb(var(--st-900) / 0.35);
    scrollbar-width: thin;
    -webkit-overflow-scrolling: touch;
  }

  .matrix-scrollbar::-webkit-scrollbar {
    width: 8px;
    height: 8px;
  }

  .matrix-scrollbar::-webkit-scrollbar-track {
    background: rgb(var(--st-900) / 0.35);
  }

  .matrix-scrollbar::-webkit-scrollbar-thumb {
    min-width: 32px;
    border: 2px solid transparent;
    border-radius: 999px;
    background: rgb(var(--st-700));
    background-clip: padding-box;
  }

  .matrix-scrollbar::-webkit-scrollbar-thumb:hover {
    background: rgb(var(--st-600));
    background-clip: padding-box;
  }

  .matrix-scrollbar::-webkit-scrollbar-corner {
    background: transparent;
  }
</style>
