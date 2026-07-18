<script lang="ts">
	// Low-level shimmer block. Compose these to mirror a page's real layout
	// while its data loads, so content settles into place with no jump.
	let {
		width = '100%',
		height = '1rem',
		rounded = 'rounded',
		class: klass = ''
	}: {
		width?: string;
		height?: string;
		rounded?: string;
		class?: string;
	} = $props();
</script>

<div class={`skeleton ${rounded} ${klass}`} style={`width: ${width}; height: ${height};`}></div>
