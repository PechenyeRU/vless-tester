<script>
	// Help renders a small ⓘ affordance with a daisyUI tooltip. Use it next to
	// section titles and controls to explain what they do.
	let { tip, pos = 'top' } = $props();
</script>

<span
	class="tooltip tooltip-{pos} align-middle ml-1 cursor-help text-base-content/40 hover:text-base-content/80"
	data-tip={tip}
	role="img"
	aria-label={tip}
>
	ⓘ
</span>
