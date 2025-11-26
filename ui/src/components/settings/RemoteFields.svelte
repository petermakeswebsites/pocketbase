<script>
    import { onMount } from "svelte";
    import { slide } from "svelte/transition";
    import ApiClient from "@/utils/ApiClient";
    import { removeError } from "@/stores/errors";
    import Field from "@/components/base/Field.svelte";
    import RedactedPasswordInput from "@/components/base/RedactedPasswordInput.svelte";
    import ObjectSelect from "@/components/base/ObjectSelect.svelte";

    const testRequestKey = "remote_test_request";

    export let originalConfig = {};
    export let config = {};
    export let configKey = "remote";
    export let toggleLabel = "Enable FTP/SFTP storage";
    export let testFilesystem = "storage"; // storage or backups
    export let testError = null;
    export let isTesting = false;

    let testTimeoutId = null;
    let testDebounceId = null;
    let maskPassword = false;

    $: if (originalConfig?.enabled) {
        refreshMaskPassword();
        testConnectionWithDebounce(100);
    }

    // clear errors on disable
    $: if (!config.enabled) {
        removeError(configKey);
    }

    function refreshMaskPassword() {
        maskPassword = !!originalConfig?.password;
    }

    function testConnectionWithDebounce(timeout) {
        isTesting = true;
        clearTimeout(testDebounceId);
        testDebounceId = setTimeout(() => {
            testConnection();
        }, timeout);
    }

    async function testConnection() {
        testError = null;

        if (!config.enabled) {
            isTesting = false;
            return testError;
        }

        // auto cancel the test request after 30sec
        ApiClient.cancelRequest(testRequestKey);
        clearTimeout(testTimeoutId);
        testTimeoutId = setTimeout(() => {
            ApiClient.cancelRequest(testRequestKey);
            testError = new Error("Test connection timeout.");
            isTesting = false;
        }, 30000);

        isTesting = true;

        let err;

        try {
            // Ensure you've added the corresponding testRemote endpoint to your ApiClient
            await ApiClient.settings.testRemote(testFilesystem, {
                $cancelKey: testRequestKey,
            });
        } catch (e) {
            err = e;
        }

        if (!err?.isAbort) {
            testError = err;
            isTesting = false;
            clearTimeout(testTimeoutId);
        }

        return testError;
    }

    onMount(() => {
        return () => {
            clearTimeout(testTimeoutId);
            clearTimeout(testDebounceId);
        };
    });
</script>

<Field class="form-field form-field-toggle" let:uniqueId>
    <input type="checkbox" id={uniqueId} required bind:checked={config.enabled} />
    <label for={uniqueId}>{toggleLabel}</label>
</Field>

<slot {isTesting} {testError} enabled={config.enabled} />

{#if config.enabled}
    <div class="grid" transition:slide={{ duration: 150 }}>
        <div class="col-lg-6">
            <Field class="form-field required" name="{configKey}.host" let:uniqueId>
                <label for={uniqueId}>Host</label>
                <input type="text" id={uniqueId} required bind:value={config.host} />
            </Field>
        </div>
        <div class="col-lg-3">
            <Field class="form-field required" name="{configKey}.port" let:uniqueId>
                <label for={uniqueId}>Port</label>
                <input type="number" id={uniqueId} required bind:value={config.port} />
            </Field>
        </div>
        <div class="col-lg-3">
            <Field class="form-field required" name="{configKey}.type" let:uniqueId>
                <label for={uniqueId}>Protocol</label>
                <ObjectSelect
                    id={uniqueId}
                    items={['ftp', 'sftp']}
                    bind:keyOfSelected={config.type}
                />
            </Field>
        </div>
        <div class="col-lg-6">
            <Field class="form-field required" name="{configKey}.user" let:uniqueId>
                <label for={uniqueId}>User</label>
                <input type="text" id={uniqueId} required bind:value={config.user} />
            </Field>
        </div>
        <div class="col-lg-6">
            <Field class="form-field required" name="{configKey}.password" let:uniqueId>
                <label for={uniqueId}>Password</label>
                <RedactedPasswordInput
                    required
                    id={uniqueId}
                    bind:mask={maskPassword}
                    bind:value={config.password}
                />
            </Field>
        </div>
        <div class="col-lg-12" />
    </div>
{/if}
