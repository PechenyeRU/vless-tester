<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { auth } from '$lib/auth.svelte.js';

	let username = $state('');
	let password = $state('');
	let show = $state(false);
	let error = $state('');
	let busy = $state(false);
	let usernameInput;

	// Focus the first field on mount instead of the `autofocus` attribute, which
	// the a11y linter flags.
	onMount(() => usernameInput?.focus());

	async function submit(e) {
		e.preventDefault();
		error = '';
		busy = true;
		try {
			await auth.login(username, password);
			goto('/');
		} catch (err) {
			error = err.message;
		} finally {
			busy = false;
		}
	}
</script>

<div class="min-h-screen grid place-items-center p-4">
	<div class="card w-full max-w-sm bg-base-100 shadow-xl">
		<div class="card-body">
			<div class="flex items-center gap-3 mb-2">
				<div class="avatar avatar-placeholder">
					<div class="bg-primary text-primary-content w-12 rounded-xl grid place-items-center">
						<span class="text-xl font-bold">v</span>
					</div>
				</div>
				<div>
					<h1 class="text-lg font-bold leading-tight">vless-tester</h1>
					<p class="text-sm text-base-content/60">Sign in to the admin console</p>
				</div>
			</div>

			<form onsubmit={submit} class="flex flex-col gap-3">
				<label class="form-control w-full">
					<span class="label-text mb-1">Username</span>
					<input
						bind:this={usernameInput}
						class="input input-bordered w-full"
						type="text"
						bind:value={username}
						placeholder="admin"
						autocomplete="username"
					/>
				</label>

				<label class="form-control w-full">
					<span class="label-text mb-1">Password</span>
					<div class="join w-full">
						<input
							class="input input-bordered join-item w-full"
							type={show ? 'text' : 'password'}
							bind:value={password}
							placeholder="••••••••"
							autocomplete="current-password"
						/>
						<button
							type="button"
							class="btn join-item"
							onclick={() => (show = !show)}
							aria-label={show ? 'hide password' : 'show password'}
						>
							{show ? 'Hide' : 'Show'}
						</button>
					</div>
				</label>

				{#if error}
					<div class="alert alert-error py-2 text-sm">
						<span>{error}</span>
					</div>
				{/if}

				<button class="btn btn-primary w-full" type="submit" disabled={busy}>
					{#if busy}<span class="loading loading-spinner loading-sm"></span>{/if}
					Sign in
				</button>
			</form>
		</div>
	</div>
</div>
