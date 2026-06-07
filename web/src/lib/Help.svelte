<script>
	// Help renders a small ⓘ affordance with an explanatory tooltip. The bubble is
	// rendered as a position:fixed element with a very high z-index, positioned via
	// JS at the icon's viewport coordinates on hover/focus. Unlike a pure-CSS
	// tooltip it is not clipped by ancestor overflow:hidden (the cards) nor buried
	// under sibling stacking contexts, so it always floats above everything.
	let { tip, pos = 'top' } = $props();

	let show = $state(false);
	let x = $state(0);
	let y = $state(0);
	let anchor;

	function place() {
		const r = anchor.getBoundingClientRect();
		x = r.left + r.width / 2;
		y = pos === 'bottom' ? r.bottom + 8 : r.top - 8;
		show = true;
	}
	function hide() {
		show = false;
	}
</script>

<span
	bind:this={anchor}
	class="align-middle ml-1 cursor-help text-base-content/40 hover:text-base-content/80"
	role="img"
	aria-label={tip}
	tabindex="0"
	onmouseenter={place}
	onmouseleave={hide}
	onfocus={place}
	onblur={hide}
>
	ⓘ
</span>

{#if show}
	<div
		class="fixed z-[9999] max-w-xs whitespace-normal rounded bg-neutral px-2 py-1 text-xs leading-snug text-neutral-content shadow-lg pointer-events-none"
		style="left: {x}px; top: {y}px; transform: translate(-50%, {pos === 'bottom' ? '0' : '-100%'});"
		role="tooltip"
	>
		{tip}
	</div>
{/if}
