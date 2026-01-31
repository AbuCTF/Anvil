<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api } from '$lib/api';
  import { auth } from '$lib/stores/auth';
  import Icon from '@iconify/svelte';

  let activeTab = 'overview';
  let loading = true;
  let stats: any = null;
  let users: any[] = [];
  let challenges: any[] = [];
  let error = '';
  let showCreateModal = false;

  // Challenge creation
  let challengeType: 'docker' | 'ova' = 'docker';
  let challengeName = '';
  let challengeDescription = '';
  let challengeDifficulty = 'easy';
  let challengePoints = 100;
  let dockerImage = '';
  let singleFlag = '';
  
  // OVA specific
  let ovaFile: File | null = null;
  let ovaFlags = [
    { name: 'User Flag', flag: '', points: 50 },
    { name: 'Root Flag', flag: '', points: 50 }
  ];
  
  let uploadLoading = false;
  let uploadError = '';
  let uploadProgress = 0;

  onMount(async () => {
    await auth.checkAuth();
    if (!$auth.isAuthenticated || $auth.user?.role !== 'admin') {
      goto('/');
      return;
    }
    await loadDashboard();
  });

  async function loadDashboard() {
    loading = true;
    error = '';
    try {
      const [statsRes, usersRes, challengesRes] = await Promise.all([
        api.getAdminStats(),
        api.getAdminUsers(),
        api.getAdminChallenges()
      ]);
      stats = statsRes;
      users = usersRes?.users || [];
      challenges = challengesRes?.challenges || [];
    } catch (e) {
      console.error('Failed to load dashboard:', e);
      error = e instanceof Error ? e.message : 'Failed to load dashboard';
    } finally {
      loading = false;
    }
  }

  function addFlag() {
    ovaFlags = [...ovaFlags, { name: 'Flag ' + (ovaFlags.length + 1), flag: '', points: 25 }];
  }

  function removeFlag(index: number) {
    ovaFlags = ovaFlags.filter((_, i) => i !== index);
  }

  function handleFileSelect(event: Event) {
    const input = event.target as HTMLInputElement;
    if (input.files && input.files[0]) {
      ovaFile = input.files[0];
    }
  }

  function formatFileSize(bytes: number): string {
    if (bytes >= 1073741824) return (bytes / 1073741824).toFixed(2) + ' GB';
    if (bytes >= 1048576) return (bytes / 1048576).toFixed(2) + ' MB';
    return (bytes / 1024).toFixed(2) + ' KB';
  }

  async function handleCreateChallenge() {
    uploadLoading = true;
    uploadError = '';
    
    try {
      if (challengeType === 'docker') {
        await api.createAdminChallenge({
          name: challengeName,
          description: challengeDescription,
          difficulty: challengeDifficulty,
          base_points: challengePoints,
          challenge_type: 'docker',
          container_image: dockerImage,
          flag: singleFlag
        });
      } else {
        // OVA upload
        if (!ovaFile) {
          uploadError = 'Please select an OVA file';
          uploadLoading = false;
          return;
        }
        
        const formData = new FormData();
        formData.append('file', ovaFile);
        formData.append('name', challengeName);
        formData.append('description', challengeDescription);
        formData.append('difficulty', challengeDifficulty);
        formData.append('base_points', challengePoints.toString());
        formData.append('flags', JSON.stringify(ovaFlags));
        
        await api.uploadOvaChallenge(formData, (progress: number) => {
          uploadProgress = progress;
        });
      }
      
      showCreateModal = false;
      await loadDashboard();
      resetForm();
    } catch (e) {
      uploadError = e instanceof Error ? e.message : 'Failed to create challenge';
    } finally {
      uploadLoading = false;
      uploadProgress = 0;
    }
  }

  function resetForm() {
    challengeName = '';
    challengeDescription = '';
    challengeDifficulty = 'easy';
    challengePoints = 100;
    dockerImage = '';
    singleFlag = '';
    ovaFile = null;
    ovaFlags = [
      { name: 'User Flag', flag: '', points: 50 },
      { name: 'Root Flag', flag: '', points: 50 }
    ];
  }

  async function publishChallenge(challenge: any) {
    try {
      await api.publishChallenge(challenge.id);
      await loadDashboard();
    } catch (e) {
      alert(e instanceof Error ? e.message : 'Failed to publish challenge');
    }
  }

  async function unpublishChallenge(challenge: any) {
    try {
      await api.unpublishChallenge(challenge.id);
      await loadDashboard();
    } catch (e) {
      alert(e instanceof Error ? e.message : 'Failed to unpublish challenge');
    }
  }

  async function deleteChallenge(id: string) {
    if (!confirm('Are you sure you want to delete this challenge?')) return;
    try {
      await api.deleteChallenge(id);
      await loadDashboard();
    } catch (e) {
      alert(e instanceof Error ? e.message : 'Failed to delete challenge');
    }
  }
</script>

<svelte:head>
  <title>Admin Dashboard - Anvil</title>
</svelte:head>

<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
  <div class="mb-8">
    <div class="flex items-center gap-4">
      <div class="w-12 h-12 bg-stone-800 border border-stone-700 rounded-xl flex items-center justify-center">
        <Icon icon="mdi:shield-crown" class="w-6 h-6 text-stone-300" />
      </div>
      <div>
        <h1 class="text-3xl font-bold text-white">Admin Dashboard</h1>
        <p class="text-stone-500 text-sm">Manage challenges, users, and platform settings</p>
      </div>
    </div>
  </div>

  {#if loading}
    <div class="flex items-center justify-center min-h-[40vh]">
      <Icon icon="mdi:loading" class="w-8 h-8 text-stone-500 animate-spin" />
    </div>
  {:else if error}
    <div class="bg-red-950/20 border border-red-900/50 rounded-lg p-4 text-center">
      <p class="text-red-400">{error}</p>
      <button on:click={loadDashboard} class="mt-2 text-sm text-stone-400 hover:text-white">Try again</button>
    </div>
  {:else}
    <!-- Tab Navigation -->
    <div class="flex gap-1 mb-6 bg-stone-900/50 border border-stone-800 rounded-lg p-1">
      {#each [
        { id: 'overview', label: 'Overview', icon: 'mdi:view-dashboard' },
        { id: 'challenges', label: 'Challenges', icon: 'mdi:flag' },
        { id: 'users', label: 'Users', icon: 'mdi:account-group' }
      ] as tab}
        <button
          type="button"
          on:click={() => activeTab = tab.id}
          class="flex items-center gap-2 px-4 py-2 rounded-md text-sm font-medium transition-all {activeTab === tab.id ? 'bg-stone-800 text-white' : 'text-stone-400 hover:text-white hover:bg-stone-800/50'}"
        >
          <Icon icon={tab.icon} class="w-4 h-4" />
          {tab.label}
        </button>
      {/each}
    </div>

    <!-- Overview Tab -->
    {#if activeTab === 'overview'}
      <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
        {#each [
          { label: 'Total Users', value: stats?.total_users || 0, icon: 'mdi:account-group' },
          { label: 'Challenges', value: stats?.total_challenges || 0, icon: 'mdi:flag' },
          { label: 'Active Instances', value: stats?.active_instances || 0, icon: 'mdi:server' },
          { label: 'Total Solves', value: stats?.total_solves || 0, icon: 'mdi:check-circle' }
        ] as stat}
          <div class="bg-stone-900/50 border border-stone-800 rounded-lg p-5">
            <div class="w-10 h-10 bg-stone-800 border border-stone-700 rounded-lg flex items-center justify-center mb-3">
              <Icon icon={stat.icon} class="w-5 h-5 text-stone-400" />
            </div>
            <p class="text-stone-500 text-xs uppercase tracking-wider mb-1">{stat.label}</p>
            <p class="text-2xl font-bold text-white">{stat.value}</p>
          </div>
        {/each}
      </div>

      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div class="bg-stone-900/50 border border-stone-800 rounded-lg p-5">
          <h2 class="text-lg font-semibold text-white mb-4">Recent Users</h2>
          {#if users.length === 0}
            <p class="text-stone-500 text-sm">No users yet</p>
          {:else}
            <div class="space-y-2">
              {#each users.slice(0, 5) as user}
                <div class="flex items-center justify-between p-3 bg-stone-800/50 border border-stone-700/50 rounded-lg">
                  <div class="flex items-center gap-3">
                    <div class="w-8 h-8 bg-stone-700 rounded-full flex items-center justify-center">
                      <span class="text-xs font-bold text-white">{user.username?.charAt(0)?.toUpperCase() || '?'}</span>
                    </div>
                    <div>
                      <p class="text-white text-sm font-medium">{user.username}</p>
                      <p class="text-stone-500 text-xs">{user.email}</p>
                    </div>
                  </div>
                  <span class="text-stone-500 text-xs">{user.role}</span>
                </div>
              {/each}
            </div>
          {/if}
        </div>

        <div class="bg-stone-900/50 border border-stone-800 rounded-lg p-5">
          <h2 class="text-lg font-semibold text-white mb-4">Recent Challenges</h2>
          {#if challenges.length === 0}
            <p class="text-stone-500 text-sm">No challenges yet</p>
          {:else}
            <div class="space-y-2">
              {#each challenges.slice(0, 5) as challenge}
                <div class="flex items-center justify-between p-3 bg-stone-800/50 border border-stone-700/50 rounded-lg">
                  <div>
                    <p class="text-white text-sm font-medium">{challenge.name}</p>
                    <p class="text-stone-500 text-xs">{challenge.difficulty}</p>
                  </div>
                  <span class="text-stone-400 text-sm">{challenge.total_solves || 0} solves</span>
                </div>
              {/each}
            </div>
          {/if}
        </div>
      </div>
    {/if}

    <!-- Challenges Tab -->
    {#if activeTab === 'challenges'}
      <div class="bg-stone-900/50 border border-stone-800 rounded-lg overflow-hidden">
        <div class="p-4 border-b border-stone-800 flex items-center justify-between">
          <h2 class="text-lg font-semibold text-white">Challenges ({challenges.length})</h2>
          <button on:click={() => showCreateModal = true} class="px-4 py-2 bg-white text-black text-sm font-medium rounded-lg hover:bg-stone-200 transition flex items-center gap-2">
            <Icon icon="mdi:plus" class="w-4 h-4" />
            Create Challenge
          </button>
        </div>
        {#if challenges.length === 0}
          <div class="p-8 text-center">
            <Icon icon="mdi:flag-outline" class="w-12 h-12 text-stone-600 mx-auto mb-3" />
            <p class="text-stone-500">No challenges yet. Create your first challenge!</p>
          </div>
        {:else}
          <div class="divide-y divide-stone-800">
            {#each challenges as challenge}
              <div class="p-4 flex items-center justify-between hover:bg-stone-800/30">
                <div class="flex items-center gap-4">
                  <div class="w-10 h-10 bg-stone-800 rounded-lg flex items-center justify-center">
                    <Icon icon={challenge.resource_type === 'vm' ? 'mdi:server' : 'mdi:docker'} class="w-5 h-5 text-stone-400" />
                  </div>
                  <div>
                    <div class="flex items-center gap-2">
                      <p class="text-white font-medium">{challenge.name}</p>
                      {#if challenge.status === 'published'}
                        <span class="px-2 py-0.5 bg-green-500/20 text-green-400 text-xs rounded-full">Published</span>
                      {:else}
                        <span class="px-2 py-0.5 bg-yellow-500/20 text-yellow-400 text-xs rounded-full">Draft</span>
                      {/if}
                    </div>
                    <p class="text-stone-500 text-xs">{challenge.difficulty} • {challenge.total_flags || 1} flag(s) • {challenge.resource_type === 'vm' ? 'VM' : 'Docker'}</p>
                  </div>
                </div>
                <div class="flex items-center gap-4">
                  <span class="text-stone-400">{challenge.base_points} pts</span>
                  <span class="text-stone-500">{challenge.total_solves || 0} solves</span>
                  <div class="flex items-center gap-1">
                    {#if challenge.status === 'published'}
                      <button on:click={() => unpublishChallenge(challenge)} class="p-2 text-yellow-400 hover:bg-yellow-500/10 rounded-lg transition" title="Unpublish">
                        <Icon icon="mdi:eye-off" class="w-4 h-4" />
                      </button>
                    {:else}
                      <button on:click={() => publishChallenge(challenge)} class="p-2 text-green-400 hover:bg-green-500/10 rounded-lg transition" title="Publish">
                        <Icon icon="mdi:eye" class="w-4 h-4" />
                      </button>
                    {/if}
                    <button on:click={() => deleteChallenge(challenge.id)} class="p-2 text-red-400 hover:bg-red-500/10 rounded-lg transition" title="Delete">
                      <Icon icon="mdi:trash-can" class="w-4 h-4" />
                    </button>
                  </div>
                </div>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}

    <!-- Users Tab -->
    {#if activeTab === 'users'}
      <div class="bg-stone-900/50 border border-stone-800 rounded-lg overflow-hidden">
        <div class="p-4 border-b border-stone-800">
          <h2 class="text-lg font-semibold text-white">Users ({users.length})</h2>
        </div>
        {#if users.length === 0}
          <div class="p-8 text-center">
            <p class="text-stone-500">No users found</p>
          </div>
        {:else}
          <div class="divide-y divide-stone-800">
            {#each users as user}
              <div class="p-4 flex items-center justify-between hover:bg-stone-800/30">
                <div class="flex items-center gap-3">
                  <div class="w-8 h-8 bg-stone-700 rounded-full flex items-center justify-center">
                    <span class="text-xs font-bold text-white">{user.username?.charAt(0)?.toUpperCase() || '?'}</span>
                  </div>
                  <div>
                    <p class="text-white font-medium">{user.username}</p>
                    <p class="text-stone-500 text-xs">{user.email}</p>
                  </div>
                </div>
                <div class="flex items-center gap-4">
                  <span class="px-2 py-1 bg-stone-800 text-stone-300 text-xs rounded">{user.role}</span>
                  <span class="text-stone-400">{user.total_score || 0} pts</span>
                </div>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}
  {/if}
</div>

<!-- Create Challenge Modal -->
{#if showCreateModal}
  <div class="fixed inset-0 bg-black/80 flex items-center justify-center z-50 p-4 overflow-y-auto">
    <div class="bg-stone-900 border border-stone-800 rounded-xl max-w-2xl w-full my-8">
      <div class="p-5 border-b border-stone-800 flex items-center justify-between">
        <h2 class="text-xl font-bold text-white">Create Challenge</h2>
        <button on:click={() => { showCreateModal = false; resetForm(); }} class="text-stone-400 hover:text-white">
          <Icon icon="mdi:close" class="w-6 h-6" />
        </button>
      </div>
      
      <form on:submit|preventDefault={handleCreateChallenge} class="p-5 space-y-5">
        {#if uploadError}
          <div class="bg-red-950/30 border border-red-800 rounded-lg p-3 text-red-400 text-sm flex items-center gap-2">
            <Icon icon="mdi:alert-circle" class="w-5 h-5 flex-shrink-0" />
            {uploadError}
          </div>
        {/if}

        <!-- Challenge Type Selector -->
        <div>
          <label class="block text-sm font-medium text-stone-300 mb-3">Challenge Type</label>
          <div class="grid grid-cols-2 gap-3">
            <button
              type="button"
              on:click={() => challengeType = 'docker'}
              class="p-4 border rounded-xl text-left transition-all {challengeType === 'docker' ? 'border-white bg-stone-800' : 'border-stone-700 hover:border-stone-600'}"
            >
              <Icon icon="mdi:docker" class="w-8 h-8 {challengeType === 'docker' ? 'text-white' : 'text-stone-500'} mb-2" />
              <p class="font-medium {challengeType === 'docker' ? 'text-white' : 'text-stone-400'}">Docker Container</p>
              <p class="text-xs text-stone-500 mt-1">Web, Crypto, Misc challenges</p>
            </button>
            <button
              type="button"
              on:click={() => challengeType = 'ova'}
              class="p-4 border rounded-xl text-left transition-all {challengeType === 'ova' ? 'border-white bg-stone-800' : 'border-stone-700 hover:border-stone-600'}"
            >
              <Icon icon="mdi:server" class="w-8 h-8 {challengeType === 'ova' ? 'text-white' : 'text-stone-500'} mb-2" />
              <p class="font-medium {challengeType === 'ova' ? 'text-white' : 'text-stone-400'}">Boot2Root VM</p>
              <p class="text-xs text-stone-500 mt-1">OVA/QCOW2 with multiple flags</p>
            </button>
          </div>
        </div>

        <!-- Common Fields -->
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div class="md:col-span-2">
            <label class="block text-sm font-medium text-stone-300 mb-2">Challenge Name</label>
            <input type="text" bind:value={challengeName} required class="w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white focus:border-stone-500 focus:outline-none" placeholder="Enter challenge name" />
          </div>
          <div>
            <label class="block text-sm font-medium text-stone-300 mb-2">Difficulty</label>
            <select bind:value={challengeDifficulty} class="w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white focus:border-stone-500 focus:outline-none">
              <option value="easy">Easy</option>
              <option value="medium">Medium</option>
              <option value="hard">Hard</option>
              <option value="insane">Insane</option>
            </select>
          </div>
          <div>
            <label class="block text-sm font-medium text-stone-300 mb-2">Base Points</label>
            <input type="number" bind:value={challengePoints} min="1" class="w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white focus:border-stone-500 focus:outline-none" />
          </div>
        </div>

        <!-- Docker-specific fields -->
        {#if challengeType === 'docker'}
          <div>
            <label class="block text-sm font-medium text-stone-300 mb-2">Docker Image</label>
            <input type="text" bind:value={dockerImage} required class="w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white font-mono focus:border-stone-500 focus:outline-none" placeholder="registry/image:tag" />
          </div>
          <div>
            <label class="block text-sm font-medium text-stone-300 mb-2">Flag</label>
            <input type="text" bind:value={singleFlag} required class="w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white font-mono focus:border-stone-500 focus:outline-none" placeholder="flag&#123;your_flag_here&#125;" />
          </div>
        {/if}

        <!-- OVA-specific fields -->
        {#if challengeType === 'ova'}
          <!-- File Upload -->
          <div>
            <label class="block text-sm font-medium text-stone-300 mb-2">OVA/QCOW2 File</label>
            <div class="border-2 border-dashed border-stone-700 rounded-xl p-6 text-center hover:border-stone-500 transition-colors">
              {#if ovaFile}
                <div class="flex items-center justify-center gap-4">
                  <Icon icon="mdi:file-check" class="w-10 h-10 text-green-400" />
                  <div class="text-left">
                    <p class="text-white font-medium">{ovaFile.name}</p>
                    <p class="text-stone-500 text-sm">{formatFileSize(ovaFile.size)}</p>
                  </div>
                  <button type="button" on:click={() => ovaFile = null} class="text-red-400 hover:text-red-300 p-2 ml-4">
                    <Icon icon="mdi:close" class="w-5 h-5" />
                  </button>
                </div>
              {:else}
                <Icon icon="mdi:cloud-upload" class="w-12 h-12 text-stone-500 mx-auto mb-3" />
                <p class="text-stone-400 mb-3">Drop your VM file here or click to browse</p>
                <input type="file" accept=".ova,.qcow2,.vmdk" on:change={handleFileSelect} class="hidden" id="ova-file" />
                <label for="ova-file" class="inline-block px-4 py-2 bg-stone-800 text-white rounded-lg cursor-pointer hover:bg-stone-700 transition-colors">
                  Select File
                </label>
                <p class="text-stone-600 text-xs mt-3">Supported: .ova, .qcow2, .vmdk (max 20GB)</p>
              {/if}
            </div>
          </div>

          <!-- Multi-flag configuration -->
          <div>
            <div class="flex items-center justify-between mb-3">
              <label class="text-sm font-medium text-stone-300">Flags ({ovaFlags.length})</label>
              <button type="button" on:click={addFlag} class="text-sm text-stone-400 hover:text-white flex items-center gap-1">
                <Icon icon="mdi:plus" class="w-4 h-4" />
                Add Flag
              </button>
            </div>
            <div class="space-y-3">
              {#each ovaFlags as flag, i}
                <div class="bg-black border border-stone-700 rounded-lg p-4">
                  <div class="flex items-center gap-3 mb-3">
                    <input type="text" bind:value={flag.name} class="flex-1 px-3 py-2 bg-stone-900 border border-stone-700 rounded-lg text-white text-sm" placeholder="Flag name (e.g., User Flag)" />
                    <input type="number" bind:value={flag.points} min="1" class="w-24 px-3 py-2 bg-stone-900 border border-stone-700 rounded-lg text-white text-sm text-center" placeholder="Points" />
                    {#if ovaFlags.length > 1}
                      <button type="button" on:click={() => removeFlag(i)} class="text-red-400 hover:text-red-300 p-1">
                        <Icon icon="mdi:trash-can" class="w-5 h-5" />
                      </button>
                    {/if}
                  </div>
                  <input type="text" bind:value={flag.flag} required class="w-full px-3 py-2 bg-stone-900 border border-stone-700 rounded-lg text-white font-mono text-sm" placeholder="flag&#123;...&#125;" />
                </div>
              {/each}
            </div>
            <p class="text-stone-500 text-xs mt-2">Total points: {ovaFlags.reduce((sum, f) => sum + (f.points || 0), 0)}</p>
          </div>
        {/if}

        <!-- Description -->
        <div>
          <label class="block text-sm font-medium text-stone-300 mb-2">Description</label>
          <textarea bind:value={challengeDescription} rows="3" class="w-full px-4 py-3 bg-black border border-stone-700 rounded-lg text-white resize-none focus:border-stone-500 focus:outline-none" placeholder="Challenge description..."></textarea>
        </div>

        <!-- Upload Progress -->
        {#if uploadLoading && challengeType === 'ova' && uploadProgress > 0}
          <div class="bg-stone-800 rounded-full h-2 overflow-hidden">
            <div class="bg-white h-full transition-all duration-300" style="width: {uploadProgress}%"></div>
          </div>
          <p class="text-center text-stone-400 text-sm">Uploading... {uploadProgress}%</p>
        {/if}

        <!-- Actions -->
        <div class="flex gap-3 pt-3 border-t border-stone-800">
          <button type="submit" disabled={uploadLoading} class="flex-1 py-3 bg-white text-black font-medium rounded-lg hover:bg-stone-200 disabled:opacity-50 flex items-center justify-center gap-2">
            {#if uploadLoading}
              <Icon icon="mdi:loading" class="w-5 h-5 animate-spin" />
              {challengeType === 'ova' ? 'Uploading...' : 'Creating...'}
            {:else}
              <Icon icon="mdi:check" class="w-5 h-5" />
              Create Challenge
            {/if}
          </button>
          <button type="button" on:click={() => { showCreateModal = false; resetForm(); }} class="px-6 py-3 bg-stone-800 text-stone-300 rounded-lg hover:bg-stone-700 border border-stone-700">
            Cancel
          </button>
        </div>
      </form>
    </div>
  </div>
{/if}
