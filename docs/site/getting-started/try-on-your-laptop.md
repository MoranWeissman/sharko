# Try it on your laptop with kind

This page builds a small test setup on your own computer, with nothing to pay for. You get two Kubernetes clusters that run in Docker: a **hub** where ArgoCD and Sharko run, and a **spoke** that Sharko registers and manages.

Use this for learning and testing only. Do not use it for production.

## What you need

- [Docker](https://docs.docker.com/get-docker/), running
- [kind](https://kind.sigs.k8s.io/) v0.24 or newer. The node image below is built for kind v0.24 and newer.
- `kubectl`, `helm` (3.x) and the `argocd` command line tool
- A Git repository for Sharko to write to, and a personal access token for it (see [First Run](first-run.md))

Every command below names its cluster with `--context` (or `--kube-context` for Helm). This way a command can never run against another cluster in your kubeconfig by mistake.

## 1. Make the two clusters

Both clusters use the same node image, Kubernetes 1.31. That is the version the docs say Sharko is tested on (see [Operator prerequisites](../operator/installation.md#prerequisites)).

```bash
kind create cluster --name hub --image kindest/node:v1.31.0
kind create cluster --name spoke-1 --image kindest/node:v1.31.0
```

kind adds the contexts `kind-hub` and `kind-spoke-1` to your kubeconfig. Check that both clusters are ready:

```bash
kubectl get nodes --context kind-hub
kubectl get nodes --context kind-spoke-1
```

## 2. Install ArgoCD on the hub

This is the test setup from [Install ArgoCD for Sharko](../operator/install-argocd.md), with the context added. It turns off ArgoCD's TLS server, which is fine for a laptop test.

```bash
helm repo add argo https://argoproj.github.io/argo-helm --force-update
helm install argocd argo/argo-cd \
  --version 10.9.6 \
  --namespace argocd --create-namespace \
  --set configs.params."server\.insecure"=true \
  --kube-context kind-hub
```

`--force-update` makes `helm repo add` work even if an old helm setup in your home folder already has a repository called `argo`. Without it, you could see an "already exists" message. That message is fine and you can go on.

Wait for it to be ready:

```bash
kubectl wait --for=condition=available --timeout=180s \
  deployment/argocd-server -n argocd --context kind-hub
```

## 3. Install Sharko on the hub

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --namespace sharko --create-namespace \
  --kube-context kind-hub
```

Check the pod:

```bash
kubectl get pods -n sharko --context kind-hub
```

## 4. Make the ArgoCD token

Follow [Make the ArgoCD token first](first-run.md#make-the-argocd-token-first). Use `kind-hub` as the ArgoCD context.

The simplest way is the ArgoCD web page: log in as `admin`, then go to **Settings**, **Accounts**, **sharko**, and under **Tokens** click **Generate New**. Copy the token right away. ArgoCD shows it only once. To open the web page, run this in a second terminal and go to [http://localhost:8081](http://localhost:8081):

```bash
kubectl port-forward svc/argocd-server 8081:80 -n argocd --context kind-hub
```

The `admin` password is saved to a private file by the steps in the first-run page (`umask 077`, in `~/argocd-admin-password.txt`). The other way, the command line, saves the token to the private file `~/sharko-argocd-token.txt`. Both ways are described there, including when to delete the files.

## 5. Open Sharko and run the wizard

Save the admin password to a private file. The `umask 077` part makes the file readable by you only, and nothing is printed:

```bash
(umask 077; kubectl get secret sharko-initial-admin-secret -n sharko --context kind-hub \
  -o jsonpath='{.data.password}' | base64 -d > ~/sharko-admin-password.txt)
```

The password is in `~/sharko-admin-password.txt`. You must type it on the login page, so the next command shows it on the screen. Do it where nobody is looking:

```bash
cat ~/sharko-admin-password.txt
```

Clear the terminal with `clear` after you have copied it. When you are logged in, delete the file with `rm ~/sharko-admin-password.txt`.

Open the page:

```bash
kubectl port-forward svc/sharko 8080:80 -n sharko --context kind-hub
```

Go to [http://localhost:8080](http://localhost:8080) and log in as `admin`. Then follow [First Run](first-run.md). In step 3, set **Secrets Provider** to **Kubernetes Secrets**. Sharko will then read a cluster's kubeconfig from a Kubernetes Secret in the `sharko` namespace.

## 6. Give Sharko the spoke's kubeconfig

Sharko runs inside the hub. The normal kubeconfig that kind makes for `spoke-1` points at `127.0.0.1` on your computer. From inside the hub, that address is the hub itself, so Sharko could not reach the spoke.

The `--internal` flag gives the address that works between the two clusters: the name of the spoke's node on the Docker network (`spoke-1-control-plane:6443`).

Save the kubeconfig to a private file, make the Secret from it, and delete the file. Nothing is printed:

```bash
(umask 077; kind get kubeconfig --internal --name spoke-1 > spoke-1.kubeconfig)

kubectl create secret generic spoke-1 \
  --namespace sharko --context kind-hub \
  --from-file=kubeconfig=spoke-1.kubeconfig

rm spoke-1.kubeconfig
```

This is the shape Sharko expects:

| Part | Value |
|------|-------|
| Namespace | `sharko` (the namespace Sharko is installed in) |
| Secret name | the cluster name, here `spoke-1` |
| Key | `kubeconfig` |
| Labels | none needed |

The kubeconfig holds a private key. Do not paste it into chats or tickets.

## 7. Register the spoke

In Sharko, open **Clusters** and click **Add Cluster**.

1. Leave **Connection managed by** as it is.
2. Under **Connection source**, choose **Kubeconfig from a secrets backend**.
3. Set **Cluster Name** to `spoke-1`. The name is also the Secret name, so the Secret from step 6 is found. The **Secret name / path** field can stay empty.
4. Click **Preview**. You should see one new file, `managed-clusters.yaml`.
5. Click **Register**.

Sharko opens a pull request. Merge it in Git. Sharko merges it for you only if you turned on **Auto-merge PRs** in **Settings**, **GitOps**. Picking **Set Up & Auto-Merge** in the first-run wizard does not turn that on; it only merged the setup pull request. After that, `spoke-1` shows as **Connected** on the Clusters page, and in ArgoCD under **Settings, Clusters** with the address `https://spoke-1-control-plane:6443`.

## Clean up

```bash
kind delete cluster --name spoke-1
kind delete cluster --name hub
```

## What's next

- [Add addons to the catalog](../user-guide/addons.md)
- [Register more clusters](../user-guide/clusters.md)
