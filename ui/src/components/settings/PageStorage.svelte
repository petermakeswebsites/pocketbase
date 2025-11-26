<script>
    import { slide } from "svelte/transition";
    import ApiClient from "@/utils/ApiClient";
    import CommonHelper from "@/utils/CommonHelper";
    import { pageTitle } from "@/stores/app";
    import { setErrors } from "@/stores/errors";
    import { removeAllToasts, addSuccessToast } from "@/stores/toasts";
    import tooltip from "@/actions/tooltip";
    import PageWrapper from "@/components/base/PageWrapper.svelte";
    import SettingsSidebar from "@/components/settings/SettingsSidebar.svelte";
    import S3Fields from "@/components/settings/S3Fields.svelte";
    import RemoteFields from "@/components/settings/RemoteFields.svelte"; //

    $pageTitle = "Files storage";
    const testRequestKey = "storage_test_request";

    let originalFormSettings = {};
    let formSettings = {};
    let isLoading = false;
    let isSaving = false;
    let isTestingS3 = false;     //
    let testErrorS3 = null;      //
    let isTestingRemote = false; //
    let testErrorRemote = null;  //

    $: initialHash = JSON.stringify(originalFormSettings);

    $: hasChanges = initialHash != JSON.stringify(formSettings);

    loadSettings();

    async function loadSettings() {
        isLoading = true;
        try {
            const settings = (await ApiClient.settings.getAll()) || {};
            init(settings);
        } catch (err) {
            ApiClient.error(err);
        }

        isLoading = false;
    }

    async function save() {
        if (isSaving || !hasChanges) {
            return;
        }

        isSaving = true;
        try {
            ApiClient.cancelRequest(testRequestKey);
            const settings = await ApiClient.settings.update(CommonHelper.filterRedactedProps(formSettings));
            setErrors({});
            await init(settings);

            removeAllToasts();

            addSuccessToast("Successfully saved storage settings.");
        } catch (err) {
            ApiClient.error(err);
        }

        isSaving = false;
    }

    async function init(settings = {}) {
        formSettings = {
            s3: settings?.s3 || {},
            remote: settings?.remote || {}, //
        };

        originalFormSettings = JSON.parse(JSON.stringify(formSettings));
    }

    async function reset() {
        formSettings = JSON.parse(JSON.stringify(originalFormSettings || {}));
    }
</script>

<SettingsSidebar />

<PageWrapper>
    <header class="page-header">
        <nav class="breadcrumbs">
            <div class="breadcrumb-item">Settings</div>
            <div class="breadcrumb-item">{$pageTitle}</div>
        </nav>
    </header>

    <div class="wrapper">
        <form class="panel" autocomplete="off" on:submit|preventDefault={() => save()}>
            <div class="content txt-xl m-b-base">
                <p>By default PocketBase uses the local file system to store uploaded files.</p>
                <p>
                    If you have limited disk space, you could optionally connect to an S3 compatible storage
                    or FTP/SFTP server.
                </p>
            </div>

            {#if isLoading}
                <div class="loader" />
            {:else}
                <S3Fields
                    toggleLabel="Use S3 storage"
                    originalConfig={originalFormSettings.s3}
                    bind:config={formSettings.s3}
                    bind:isTesting={isTestingS3}
                    bind:testError={testErrorS3}
                >
                    {#if originalFormSettings.s3?.enabled != formSettings.s3.enabled}
                        <div transition:slide={{ duration: 150 }}>
                            <div class="alert alert-warning m-0">
                                <div class="icon">
                                    <i class="ri-error-warning-line" />
                                </div>
                                <div class="content">
                                    If you have existing uploaded files, you'll have to migrate them manually.
                                </div>
                            </div>
                            <div class="clearfix m-t-base" />
                        </div>
                    {/if}
                </S3Fields>

                {#if formSettings.s3?.enabled && !hasChanges && !isSaving}
                    <div class="flex m-b-base">
                        <div class="flex-fill" />
                        {#if isTestingS3}
                            <span class="loader loader-sm" />
                        {:else if testErrorS3}
                            <div
                                class="label label-sm label-warning entrance-right"
                                use:tooltip={testErrorS3.data?.message}
                            >
                                <i class="ri-error-warning-line txt-warning" />
                                <span class="txt">Failed to establish S3 connection</span>
                            </div>
                        {:else}
                            <div class="label label-sm label-success entrance-right">
                                <i class="ri-checkbox-circle-line txt-success" />
                                <span class="txt">S3 connected successfully</span>
                            </div>
                        {/if}
                    </div>
                {/if}

                <hr class="m-t-sm m-b-sm"/>

                <RemoteFields
                    toggleLabel="Use FTP/SFTP storage"
                    originalConfig={originalFormSettings.remote}
                    bind:config={formSettings.remote}
                    bind:isTesting={isTestingRemote}
                    bind:testError={testErrorRemote}
                >
                     {#if originalFormSettings.remote?.enabled != formSettings.remote.enabled}
                        <div transition:slide={{ duration: 150 }}>
                            <div class="alert alert-warning m-0">
                                <div class="icon">
                                    <i class="ri-error-warning-line" />
                                </div>
                                <div class="content">
                                    If you have existing uploaded files, you'll have to migrate them manually.
                                </div>
                            </div>
                            <div class="clearfix m-t-base" />
                        </div>
                    {/if}
                </RemoteFields>

                {#if formSettings.remote?.enabled && !hasChanges && !isSaving}
                    <div class="flex m-b-base">
                        <div class="flex-fill" />
                        {#if isTestingRemote}
                            <span class="loader loader-sm" />
                        {:else if testErrorRemote}
                            <div
                                class="label label-sm label-warning entrance-right"
                                use:tooltip={testErrorRemote.data?.message}
                            >
                                <i class="ri-error-warning-line txt-warning" />
                                <span class="txt">Failed to establish connection</span>
                            </div>
                        {:else}
                            <div class="label label-sm label-success entrance-right">
                                <i class="ri-checkbox-circle-line txt-success" />
                                <span class="txt">FTP/SFTP connected successfully</span>
                            </div>
                        {/if}
                    </div>
                {/if}

                {#if formSettings.s3?.enabled && formSettings.remote?.enabled}
                     <div class="alert alert-info m-t-base" transition:slide>
                        <div class="icon"><i class="ri-information-line"></i></div>
                        <div class="content">
                            Both S3 and Remote storage are enabled. <strong>S3 will take precedence</strong> and be used as the active storage system.
                        </div>
                    </div>
                {/if}

                <div class="flex m-t-base">
                    <div class="flex-fill" />
                    {#if hasChanges}
                        <button
                            type="button"
                            class="btn btn-transparent btn-hint"
                            disabled={isSaving}
                            on:click={() => reset()}
                        >
                            <span class="txt">Reset</span>
                        </button>
                    {/if}

                    <button
                        type="submit"
                        class="btn btn-expanded"
                        class:btn-loading={isSaving}
                        disabled={!hasChanges || isSaving}
                        on:click={() => save()}
                    >
                        <span class="txt">Save changes</span>
                    </button>
                </div>
            {/if}
        </form>
    </div>
</PageWrapper>
