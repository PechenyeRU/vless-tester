<script>
	// DataTable is a small client-side table with clickable column sorting and
	// pagination, for already-loaded arrays (the dashboard's fleet/by-country/
	// by-worker views). columns is [{key, label, value?(row), class?}]; a column
	// with a value() accessor is sortable. The `row` snippet renders the <td>
	// cells for one row (the <tr> is provided here).
	let {
		rows = [],
		columns = [],
		perPage = 25,
		initialSort = '',
		initialDir = 'asc',
		row,
		empty = 'No data yet.'
	} = $props();

	let sort = $state(initialSort);
	let dir = $state(initialDir);
	let page = $state(1);

	const colFor = (key) => columns.find((c) => c.key === key);

	let sorted = $derived.by(() => {
		const col = colFor(sort);
		if (!col || !col.value) return rows;
		const sign = dir === 'asc' ? 1 : -1;
		return [...rows].sort((a, b) => {
			const av = col.value(a);
			const bv = col.value(b);
			if (av < bv) return -sign;
			if (av > bv) return sign;
			return 0;
		});
	});
	let pages = $derived(Math.max(1, Math.ceil(sorted.length / perPage)));
	let pageRows = $derived(sorted.slice((page - 1) * perPage, page * perPage));

	// Keep the page in range when the underlying rows shrink (e.g. a refresh).
	$effect(() => {
		if (page > pages) page = pages;
	});

	function sortBy(col) {
		if (!col.value) return;
		if (sort === col.key) {
			dir = dir === 'asc' ? 'desc' : 'asc';
		} else {
			sort = col.key;
			dir = 'asc';
		}
		page = 1;
	}
</script>

<div class="overflow-x-auto">
	<table class="table table-sm">
		<thead>
			<tr>
				{#each columns as c}
					<th
						class={c.value ? 'cursor-pointer select-none hover:text-base-content' : ''}
						onclick={() => sortBy(c)}
					>
						{c.label}{#if sort === c.key}<span class="ml-0.5">{dir === 'asc' ? '▲' : '▼'}</span>{/if}
					</th>
				{/each}
			</tr>
		</thead>
		<tbody>
			{#each pageRows as r}
				<tr class="hover">{@render row(r)}</tr>
			{:else}
				<tr><td colspan={columns.length} class="text-base-content/60 py-4 text-center">{empty}</td></tr>
			{/each}
		</tbody>
	</table>
</div>
{#if pages > 1}
	<div class="flex items-center justify-center gap-2 py-3">
		<button class="btn btn-xs" disabled={page <= 1} onclick={() => (page = Math.max(1, page - 1))}>‹</button>
		<span class="text-xs text-base-content/60">Page {page} / {pages}</span>
		<button class="btn btn-xs" disabled={page >= pages} onclick={() => (page = Math.min(pages, page + 1))}>›</button>
	</div>
{/if}
