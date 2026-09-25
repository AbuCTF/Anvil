<script lang="ts">
  import { onMount } from "svelte";
  import OpticalIcon from "$lib/components/OpticalIcon.svelte";
  import { categoryColor, rankAccent, teamColor } from "$lib/rank";

  interface MatrixChallenge {
    slug: string;
    name: string;
    category: string;
    category_color: string;
    difficulty?: string;
    points: number;
  }

  interface MatrixCell {
    s: number;
    b: number;
    p?: number;
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

  // rows arrive one server page at a time, already ranked, searched and sorted
  export let challenges: MatrixChallenge[] = [];
  export let rows: MatrixRow[] = [];
  export let teams = false;
  export let economy = false;
  export let search = "";
  export let category = "";
  export let page = 1;
  export let pages = 1;
  export let matching = rows.length;
  export let onPage: (page: number) => void = () => {};

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
  $: narrow = viewportWidth > 0 && viewportWidth < 560;
  $: nameColumnWidth = narrow
    ? 156
    : Math.min(340, Math.max(240, viewportWidth > 0 ? viewportWidth * 0.24 : 260));
  // columns share the free width, but stay tappable when crowded and don't balloon when few
  $: challengeColumnWidth = visibleChallenges.length
    ? Math.min(
        96,
        Math.max(
          44,
          (Math.max(viewportWidth, nameColumnWidth) - nameColumnWidth) /
            visibleChallenges.length,
        ),
      )
    : 44;
  $: matrixWidth =
    nameColumnWidth + challengeColumnWidth * visibleChallenges.length;
  $: hasPartial = economy && rows.some((row) => row.cells.some((cell) => cell?.p));
  $: noun = teams ? "team" : "player";
  $: summary = `${matching.toLocaleString()} ${noun}${matching === 1 ? "" : "s"} · ${visibleChallenges.length} challenge${visibleChallenges.length === 1 ? "" : "s"}`;

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

  function columnTitle(challenge: MatrixChallenge) {
    const parts = [challenge.name, challenge.category];
    if (challenge.difficulty) parts.push(challenge.difficulty);
    if (!economy) parts.push(`${challenge.points} pts`);
    return parts.join(" · ");
  }

  function cellTitle(
    row: MatrixRow,
    challenge: MatrixChallenge,
    cell: MatrixCell,
  ) {
    if (cell?.p) return `${row.name} holds part of ${challenge.name}`;
    if (!cell?.s) return `${row.name} has not solved ${challenge.name}`;
    const blood = cell.b > 0 && cell.b <= 3 ? ` · blood #${cell.b}` : "";
    return `${row.name} solved ${challenge.name}${blood}`;
  }

  function bloodClass(rank: number) {
    if (rank === 1) return "border-blood/50 bg-blood/20 text-blood";
    if (rank === 2) return "border-stone-400/60 bg-stone-400/15 text-stone-200";
    return "border-amber-700/60 bg-amber-700/20 text-amber-500";
  }

  function cellClass(cell: MatrixCell) {
    if (cell.s && cell.b > 0 && cell.b <= 3) return bloodClass(cell.b);
    if (cell.p) return "border-dashed border-up/50 text-up/70";
    if (cell.s) return "border-up/60 bg-up/[0.04] text-up";
    return "border-dashed border-stone-800 text-stone-700";
  }
</script>

{#if visibleChallenges.length === 0}
  <div class="px-4 py-10 text-center text-sm text-stone-500">
    {challenges.length ? "No challenges in this category." : "No challenges released yet."}
  </div>
{:else}
  <div
    bind:this={scrollViewport}
    class="matrix-scrollbar overflow-x-auto lg:max-h-[70vh] lg:overflow-auto"
    style={`--matrix-name-width: ${nameColumnWidth}px; --matrix-challenge-width: ${challengeColumnWidth}px;`}
  >
    <table
      class="table-fixed border-separate border-spacing-0 text-sm"
      style={`width: ${matrixWidth}px;`}
    >
      <colgroup>
        <col class="matrix-name-column" />
        {#each visibleChallenges as challenge (challenge.slug)}
          <col class="matrix-challenge-column" />
        {/each}
      </colgroup>
      <thead>
        <tr>
          <th
            rowspan="2"
            scope="col"
            class="matrix-name-column matrix-head-top metadata-label sticky left-0 top-0 z-40 border-b border-r border-stone-800 bg-stone-900 px-3 py-2.5 text-left align-bottom text-stone-500 sm:px-4"
          >
            {teams ? "Team" : "Player"}
          </th>
          {#each groups as group}
            <th
              colspan={group.count}
              scope="colgroup"
              class="matrix-head-top metadata-label sticky top-0 z-30 h-9 truncate border-b border-r border-stone-800/70 bg-stone-900 px-2 py-2 text-left"
              style="color: {group.color};"
              title={group.name}
            >
              {group.name}
            </th>
          {/each}
        </tr>
        <tr>
          {#each visibleChallenges as challenge (challenge.slug)}
            <th
              scope="col"
              class="matrix-challenge-column sticky top-9 z-30 h-12 border-b border-r border-stone-800/70 bg-stone-900 px-1 text-center font-normal"
              title={columnTitle(challenge)}
            >
              <a
                href="/challenges/{challenge.slug}"
                class="metadata-label inline-flex h-8 w-8 items-center justify-center rounded text-stone-500 hover:bg-stone-800/60 hover:text-stone-200"
                aria-label={columnTitle(challenge)}
              >
                {shortName(challenge.name)}
              </a>
            </th>
          {/each}
        </tr>
      </thead>
      <tbody>
        {#each rows as row (row.user_id)}
          <tr class="group">
            <th
              scope="row"
              class="matrix-name-column sticky left-0 z-20 h-11 border-b border-r border-stone-800/70 bg-stone-950 px-3 text-left font-normal group-hover:bg-stone-900 sm:px-4"
            >
              <div class="flex items-center gap-2 leading-none sm:gap-2.5">
                <span
                  class="min-w-[3ch] shrink-0 whitespace-nowrap text-right text-xs font-semibold tabular-nums sm:min-w-[4.5ch] sm:text-sm {rankAccent(
                    row.rank,
                  )}">{row.rank}</span
                >
                <span
                  class="hidden h-2 w-2 shrink-0 rounded-full sm:block"
                  style="background: {teamColor(row.user_id)};"
                ></span>
                <div
                  class="flex min-w-0 flex-1 flex-col gap-1 sm:flex-row sm:items-center sm:gap-2.5"
                >
                  {#if teams}
                    <span class="min-w-0 truncate text-stone-200" title={row.name}
                      >{row.name}</span
                    >
                  {:else}
                    <a
                      href="/profile/{encodeURIComponent(row.username)}"
                      class="min-w-0 truncate text-stone-200 hover:text-amber-400"
                      title={row.name}>{row.name}</a
                    >
                  {/if}
                  <span
                    class="shrink-0 text-[11px] tabular-nums text-stone-500 sm:ml-auto sm:text-xs"
                    >{Math.round(row.total).toLocaleString()}</span
                  >
                </div>
                {#if row.delta !== 0}
                  <span
                    class="{row.delta > 0
                      ? 'text-up'
                      : 'text-down'} hidden shrink-0 items-center text-[0.65rem] tabular-nums sm:inline-flex"
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
            {#each visibleChallenges as challenge (challenge.slug)}
              {@const cell = row.cells[challenge.index] ?? { s: 0, b: 0 }}
              <td
                class="matrix-challenge-column h-11 border-b border-r border-stone-800/60 bg-stone-950/40 p-1 text-center group-hover:bg-stone-900/50"
              >
                <span
                  class="mx-auto flex h-7 w-7 items-center justify-center rounded-full border leading-none {cellClass(
                    cell,
                  )}"
                  title={cellTitle(row, challenge, cell)}
                  role="img"
                  aria-label={cellTitle(row, challenge, cell)}
                >
                  {#if cell.s && cell.b > 0 && cell.b <= 3}
                    <span
                      class="blood-label text-[0.62rem] font-semibold tabular-nums"
                      >{cell.b}</span
                    >
                  {:else if cell.p}
                    <span class="h-1.5 w-1.5 rounded-full bg-current"></span>
                  {:else if cell.s}
                    <span class="h-2 w-2 rounded-full bg-current"></span>
                  {/if}
                </span>
              </td>
            {/each}
          </tr>
        {/each}
        {#if rows.length === 0}
          <tr>
            <td
              colspan={visibleChallenges.length + 1}
              class="px-4 py-10 text-left text-stone-500"
            >
              {search.trim() ? `No ${noun}s match “${search.trim()}”.` : `No ${noun}s on this page.`}
            </td>
          </tr>
        {/if}
      </tbody>
    </table>
  </div>

  <div
    class="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-stone-800 px-4 py-3 text-xs text-stone-500"
  >
    <span class="metadata-label text-stone-600">{summary}</span>
    {#if pages > 1}
      <div
        class="inline-flex items-center overflow-hidden rounded border border-stone-800"
        aria-label="Matrix pages"
      >
        <button
          type="button"
          disabled={page <= 1}
          on:click={() => onPage(page - 1)}
          class="inline-flex h-6 w-7 items-center justify-center text-stone-500 hover:text-stone-200 disabled:cursor-not-allowed disabled:opacity-30"
          aria-label="Previous matrix page"
        >
          <OpticalIcon icon="mdi:chevron-left" size={12} box={12} />
        </button>
        <span
          class="metadata-label border-x border-stone-800 px-2 py-1 text-stone-500"
          >{page}/{pages}</span
        >
        <button
          type="button"
          disabled={page >= pages}
          on:click={() => onPage(page + 1)}
          class="inline-flex h-6 w-7 items-center justify-center text-stone-500 hover:text-stone-200 disabled:cursor-not-allowed disabled:opacity-30"
          aria-label="Next matrix page"
        >
          <OpticalIcon icon="mdi:chevron-right" size={12} box={12} />
        </button>
      </div>
    {/if}
    <div class="flex flex-wrap items-center gap-x-4 gap-y-2 sm:ml-auto">
      <span class="inline-flex items-center gap-1.5 leading-none">
        <span class="h-3 w-3 rounded-full border border-up/60 bg-up/[0.04]"
        ></span>
        <span class="optical-label">Solved</span>
      </span>
      {#if hasPartial}
        <span class="inline-flex items-center gap-1.5 leading-none">
          <span class="h-3 w-3 rounded-full border border-dashed border-up/50"
          ></span>
          <span class="optical-label">Partial</span>
        </span>
      {/if}
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
  </div>
{/if}

<style>
  .matrix-name-column {
    width: var(--matrix-name-width);
    min-width: var(--matrix-name-width);
    max-width: var(--matrix-name-width);
  }

  .matrix-challenge-column {
    width: var(--matrix-challenge-width);
    min-width: var(--matrix-challenge-width);
  }

  /* sticky cells snap to whole pixels; this covers the sliver of scrolled rows above them */
  .matrix-head-top {
    box-shadow: 0 -1px 0 rgb(var(--st-900));
  }

  .matrix-scrollbar {
    overscroll-behavior-x: contain;
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
