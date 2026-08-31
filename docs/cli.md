# CLI (`cloudctl`)

```text
cloudctl auth login   --user <email> --password <pw>
cloudctl user         create/list/get/delete
cloudctl project      create/list/get/delete
cloudctl apikey       create/list/revoke
cloudctl workload     create/list/get/delete/scale/restart/exec/logs
cloudctl deployment   create/list/get/rollback
cloudctl service      create/list/get/delete
cloudctl network      create/list/get/delete/attach
cloudctl volume       create/list/get/delete/snapshot/restore
cloudctl bucket       create/list/delete
cloudctl object       put/get/list/delete
cloudctl secret       create/list/get/delete
cloudctl configmap    create/list/get/delete
cloudctl model        register/list/delete
cloudctl run          --model <m> --replicas <n> --cpu <n> --memory <n> <name>
cloudctl node         list/get/cordon/drain
cloudctl metrics      top/workload/node
cloudctl logs         -f <workload>
cloudctl dashboard    open
```

## Configuration

Config lives in `~/.cloudctl/config.yaml`. The CLI supports multiple
profiles (e.g. `dev`, `prod`) and `CLOUDCTL_PROFILE=prod cloudctl ...`
selects one.

```yaml
active: dev
profiles:
  dev:
    endpoint: http://localhost:8443
    api_key:  ctlk_dev_xxx
  prod:
    endpoint: https://cloud.example.com
    api_key:  ctlk_prod_xxx
```
