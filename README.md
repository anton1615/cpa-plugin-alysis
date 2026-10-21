# CLIProxyAPI Alysis Provider Plugin

A native [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) plugin that
exposes the **Alysis Code** hosted models (`deepseek-flash`, `glm-5.3-flash`,
`gpt-6-luna`, `claude-sonnet-5-5`, `claude-haiku-5-5`) to the host as a regular
provider, together with a device-code login flow for the Management Center.

The plugin is a single Go module with no vendored host source: it talks to the
host over the public JSON/C ABI and uses the public `pluginapi` SDK package.

## Features

- **Model provider** — publishes the Alysis model catalog to `/v1/models`.
- **Executor** — routes each model to the upstream endpoint family it expects:
  chat-completions, Responses, or Messages.
- **Device-code login** — the Management Center shows a device-code URL; the
  plugin requests the code, polls for approval, and hands a ready-to-use auth
  record back to the host, which persists it into the auth directory.
- **Auth file parser** — reads existing Alysis auth JSON files from the auth
  directory, so credentials can also be imported by hand.
- **Credential rotation** — an upstream `401 invalid_api_key` is reported with a
  `401` status, so the host rotates to another credential instead of disabling
  the file.

## Build

Requires Go 1.26+ and a C compiler (cgo is required for `-buildmode=c-shared`).

```sh
make build      # -> build/alysis.so | build/alysis.dylib | build/alysis.dll
make test
```

Cross-compiling needs a matching C toolchain, for example:

```sh
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC=aarch64-linux-gnu-gcc \
  go build -buildmode=c-shared -o build/alysis.so .
```

Release archives are named `alysis_<version>_<os>_<arch>.zip`.

## Install

Copy the shared library into the host plugin directory (the file name defines
the plugin id, so it must be `alysis.<ext>`), then enable it in the host
configuration:

```yaml
plugins:
  enabled: true
  dir: "/path/to/plugins"
  configs:
    alysis:
      enabled: true
      priority: 10
      # Base URL of the deployment that hosts the account service.
      # The model gateway is derived as <cloud_api_url>/llm/v1.
      cloud_api_url: "https://<your-deployment>/functions/v1"
```

Restart the host afterwards; the plugin is picked up on start.

## Configuration

| Key | Required | Description |
| --- | --- | --- |
| `enabled` | no | Enable or disable the plugin (default: disabled). |
| `priority` | no | Load order when several plugins are present. |
| `cloud_api_url` | yes for login | Base URL of the account service. The model gateway base is derived from it as `<cloud_api_url>/llm/v1`. |

## Auth files

The host stores credentials in the auth directory. An Alysis auth file looks
like this:

```json
{
  "type": "alysis",
  "api_key": "<gateway key>",
  "base_url": "<cloud_api_url>/llm/v1",
  "email": "<account email>"
}
```

`base_url` must point at the model gateway. When the account service is
reached through a different deployment, update this field accordingly.

## Device-code login

1. Open the Management Center and start the login for the `alysis` provider.
2. The host asks the plugin for a login URL; the plugin requests a device code
   from the account service and returns a URL that already carries the user
   code, so nothing has to be typed by hand.
3. Approve the code on the service page.
4. The host polls the plugin, which reports success and returns the auth record
   shown above. The host writes it into the auth directory.

## License

MIT — see [LICENSE](LICENSE).
