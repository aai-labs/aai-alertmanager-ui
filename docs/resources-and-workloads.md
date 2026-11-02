# Resources and workloads

These source-version features extend the existing alert and incident views. They are not included in the published v0.1.0 image.

Configure `prometheus.url` as described in [configuration](configuration.md). No additional database or Kubernetes credentials are needed. Without Prometheus, the basic Alertmanager UI continues to work. `DEMO=true` provides synthetic examples without connecting to a cluster.

## Views

- **Alert analytics:** the existing seven/thirty-day firing-time heatmap, firing episodes, and source coverage remain available.
- **Cluster overview:** resource context, pod states, and namespace alert history together, with links back to alerts and incidents.
- **Resources:** CPU and memory usage versus requests, limits, persistent-volume usage, and 24-hour/seven-day trends. Operators can also inspect node capacity.
- **Workloads:** searchable pods, Deployments, StatefulSets, DaemonSets, and Services. Pod details link to namespace alerts and incidents.

## Metric requirements

| Data | Existing source |
| --- | --- |
| Container CPU and memory | kubelet/cAdvisor `container_cpu_usage_seconds_total`, `container_memory_working_set_bytes` |
| Requests, limits, pod inventory and readiness | kube-state-metrics pod and container metrics |
| Node allocatable capacity | `kube_node_status_allocatable` |
| Host CPU and memory usage | Node exporter CPU modes and total/available memory, mapped through an unambiguous `node_uname_info` hostname matching a Kubernetes node |
| Volume requests and inventory | kube-state-metrics persistent-volume-claim metrics |
| Resource quotas | `kube_resourcequota`, kept separate by quota and resource |
| Mounted volume capacity and use | `kubelet_volume_stats_capacity_bytes`, `kubelet_volume_stats_used_bytes` |
| Controller replicas | kube-state-metrics Deployment, StatefulSet, and DaemonSet metrics |
| Service inventory | `kube_service_info`, `kube_service_spec_type` |
| Ready endpoints | EndpointSlice metrics plus explicit `label_kubernetes_io_service_name` association |
| Optional reachability | Existing `probe_success` with explicit `target_namespace`, `target_service`, and target/instance identity |

Standard kube-prometheus-stack installations commonly provide the core metrics. Availability varies by exporter version and collection configuration. The application does not install exporters, run probes, infer Service names from EndpointSlice prefixes, or execute investigation commands.

## Reading the data

CPU usage is a five-minute rate in cores. Memory is working-set bytes. Requests reserve resources; limits constrain individual containers. Container request totals exclude completed jobs and do not represent full scheduler accounting for init containers and pod overhead.

Node CPU is logical CPU count minus the five-minute idle rate (including I/O wait in busy time); node memory is total minus available bytes. Cluster/node usage includes host processes and is distinct from namespace container usage. Allocatable capacity is available for workloads, so host usage divided by allocatable capacity can exceed 100%. Namespace limits are not a share of cluster capacity. Missing observations or incomplete limit data are not presented as zero usage or a complete limit. A container without an observed limit may be unconfigured or have missing metric collection; the UI does not claim which.

Volume figures describe observed claims and mounted-volume metrics, not raw storage-system capacity or Ceph health. Multiple scrape copies are deduplicated before aggregation. Historical resource sampling is five minutes for 24 hours and thirty minutes for seven days; UTC timestamps are explicit. Missing samples leave gaps and short spikes can be missed.

Pod states separate ready, not ready, unscheduled, completed, failed, and unknown. Running alone does not mean ready. Workload observations older than three minutes are unknown. Zero desired controller replicas can be intentional. A Service object, ready endpoints, or ready pod does not establish end-to-end reachability. Probe results require `target_namespace` and `target_service` labels naming that Service, in addition to `target` or `instance`. Ordinary scrape `namespace`/`service` labels identify an exporter and are not trusted as the probe target. Tenant queries are scoped by `target_namespace` before retrieval. An observed target with stale data prevents an all-success result; a fresh failure remains visible.

## Access and bounds

`GET /api/resources?hours=24|168&namespace=...` and `GET /api/workloads?namespace=...` use server-owned queries. Arbitrary PromQL is not accepted. Resource collection is bounded to twenty seconds, four concurrent queries per collector, and two simultaneous collectors. Per-query limits are 20,000 series and 300,000 samples, with a 512-namespace response cap. Expensive seven-day request queries use daily chunks. Partial results carry warnings. Resources cache for sixty seconds and workloads for thirty seconds. Both caches are partitioned by authorization scope and each retains at most 16 MiB of encoded responses (64 entries maximum).

Tenant namespace restrictions apply before querying and again before returning data. Node/cluster aggregates and pod node names are operator-only. A namespace selector in operator mode is a filter, not an authorization boundary. **Deployment boundary: use one monitored cluster per application.** A federated/Thanos/Mimir source must already be restricted to that cluster; the application does not provide a multi-cluster selector. Kubernetes namespace, pod, and node identities are scoped to that one cluster. Do not point these views at an unfiltered multi-cluster source. Node exporter hostnames must match Kubernetes node names unambiguously; otherwise host usage stays unknown. Kubernetes exporters and their standard labels are optional requirements for these views, not requirements for the basic Alertmanager UI.

No notification, silence, routing, or production cluster changes are made by these views.
