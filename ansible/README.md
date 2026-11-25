# Homelab: RHEL + k3s + Argo CD

`site.yml` turns a RHEL 9 host (or Alma/Rocky 9) into a single-node k3s cluster that runs Riffle via Argo CD:

- firewalld on, with the API server, kubelet and HTTP(S) ports open and k3s's pod/service networks trusted.
- SELinux must be enforcing; k3s runs with `selinux: true` and the installer adds `k3s-selinux`.
- k3s pinned (`k3s_version`), secrets encrypted at rest, kubeconfig mode 0600.
- `deploy/bootstrap/argocd.yaml` copied into k3s's manifests directory: k3s installs Argo CD, which syncs Riffle from this repo.

```sh
cp inventory.example.yml inventory.yml   # set the host and user
ansible-galaxy collection install -r requirements.yml
ansible-playbook -i inventory.yml site.yml
```
