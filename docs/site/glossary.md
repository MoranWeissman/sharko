# Glossary

These are words you will see across this site, said in plain terms.

## Addon

A piece of software you want on your clusters, for example cert-manager or metrics-server. In Sharko, an addon is a Helm chart plus the settings that go with it.

## ApplicationSet

An Argo CD object that makes many Argo CD Applications from one template, for example one for each cluster. Sharko's engine chart uses ApplicationSets to put each addon on the clusters that asked for it.

## Argo CD

An open-source tool that makes a Kubernetes cluster match what is written in a Git repo. Sharko works next to Argo CD: Sharko writes the files, and Argo CD does the installing. Many pages spell it "ArgoCD".

## Catalog

The list of addons your team has approved. It lives in `catalog.yaml` at the root of your GitOps repo. A cluster can only get an addon that is in the catalog.

## CRD (Custom Resource Definition)

A way to add a new kind of object to Kubernetes. Many addons bring their own CRDs. Argo CD's Application and ApplicationSet are CRDs too.

## Drift

When what is on the cluster is different from what Git says. In Sharko this usually means someone changed a cluster's Argo CD connection by hand, so it no longer matches the cluster list in Git.

## ESO (External Secrets Operator)

An open-source addon that copies secrets from a store, such as AWS Secrets Manager, into Kubernetes Secrets. Sharko does not need it, because Sharko can deliver addon secrets itself. You can still run ESO as one of your addons.

## GitOps

A way of working where a Git repo holds the truth. You change a file in Git, and a tool (here, Argo CD) makes the cluster match it.

## Helm chart

A package of Kubernetes files, with settings you can change. Most addons come as Helm charts. Sharko itself is installed from a Helm chart.

## IRSA (IAM Roles for Service Accounts)

An Amazon EKS feature that gives a pod its own AWS role, so no AWS keys need to be stored. Sharko can use it to read from AWS Secrets Manager and to reach EKS clusters.

## Main cluster and managed clusters (hub and spoke)

The main cluster is the one where Sharko and Argo CD run. The managed clusters are the other clusters that get their addons from it. Some pages and diagrams call these the "hub" and the "spokes".

## PR (pull request)

A suggested change to a Git repo, which people can look at before it is merged. Sharko makes its changes to your GitOps repo by opening a PR.

## Reconcile

To compare what is on the cluster with what Git says, and fix any difference. Argo CD and Sharko both keep doing this in the background.

## SBOM (Software Bill of Materials)

A list of everything a piece of software is built from. Each Sharko release publishes SBOMs for its download files and for its container image.

## Sigstore and cosign

Sigstore is a free, public service for signing software. cosign is its command-line tool. Each Sharko release signs its container image and its Helm charts with cosign, so you can check that they really came from this project. See [Supply chain](operator/supply-chain.md).

## SLO (Service Level Objective)

A target for how fast or how reliable something should be, for example "this page loads in under one second, most of the time". See [SLOs](operator/slos.md).

## Sync

In Argo CD, a sync applies what is in Git to the cluster. In Sharko, the **Sync** button on a cluster page puts the cluster's Argo CD connection back to what Git says.

## Values file

A YAML file with settings for a Helm chart. In a Sharko repo, the settings for every cluster are in `values/global/<addon>.yaml`, and the settings for one cluster are in `values/clusters/<cluster>/<addon>.yaml`.
