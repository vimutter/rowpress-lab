# Deploying Rowpress on Render

The repository includes a Dockerfile and a `render.yaml` Blueprint. No database
or persistent disk is required. Render provides `PORT`; the server defaults to
port 10000 when it is absent locally.

## Connect GitHub

1. Sign in to the [Render Dashboard](https://dashboard.render.com/), or create
   an account using your GitHub identity.
2. Open **Account Settings**, find **Account Security**, and under **Git
   Deployment Credentials** choose **Add credential → GitHub**.
3. Install or configure the Render GitHub app. Grant it access to this
   repository. For an organization repository, an organization owner might
   need to approve the app.
4. Push this repository, including `Dockerfile` and `render.yaml`, to the
   `main` branch on GitHub.

## Create from the Blueprint

1. In the Render Dashboard, choose **New → Blueprint**.
2. Select the connected GitHub repository.
3. Render finds `render.yaml` at the repository root. Review the proposed
   `rowpress` web service. Enter secret values for `BASIC_AUTH_USERNAME` and
   `BASIC_AUTH_PASSWORD`, then apply the Blueprint.
4. Watch the first build on the service's **Deploys** page. When `/healthz`
   passes, open the generated `https://…onrender.com` URL.

The Blueprint declares the names of the two secret variables with `sync: false`,
so their values are entered in Render and are never stored in Git. Both must be
set or the server refuses to start. The `/healthz` endpoint remains public for
Render's health checks; the page and WebSocket require authentication.

The Blueprint selects Docker, the free plan, Frankfurt, the `main` branch, and
the `/healthz` HTTP health check. It deploys new commits only after their GitHub
checks pass. Change `name`, `plan`, or `region` in `render.yaml` before creating
the service if desired; Render does not allow changing a service's region
afterward.

If `rowpress` is already used in the workspace, choose another service name.
No build command or start command is needed because Render uses the Dockerfile
and its `ENTRYPOINT`.

## Render-specific behavior

- Render terminates public TLS, so the page automatically uses `wss://` for
  its WebSocket connection.
- Free web services spin down after 15 minutes without traffic. The first HTTP
  request or WebSocket connection wakes the service and can take longer.
- Deploys replace the running instance, which closes existing WebSocket
  connections. A conversion can be retried from the page.
- The server keeps uploaded data in memory and does not require a persistent
  disk.

## Manual alternative

Instead of a Blueprint, choose **New → Web Service**, select the GitHub
repository, choose the Docker runtime, branch `main`, and the desired plan and
region. Under **Advanced**, set the health check path to `/healthz` and add both
Basic Auth variables as secret environment variables. Leave the Docker command
empty so the image `ENTRYPOINT` is used.
