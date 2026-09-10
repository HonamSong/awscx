"""AWS(ECS/EC2/CloudWatch/ELBv2/AutoScaling) 조회 헬퍼."""
import datetime

from botocore.exceptions import BotoCoreError, ClientError


def short_arn(arn: str) -> str:
    return arn.rsplit("/", 1)[-1] if arn else arn


def list_clusters(ecs):
    arns = []
    for page in ecs.get_paginator("list_clusters").paginate():
        arns.extend(page["clusterArns"])
    clusters = []
    for i in range(0, len(arns), 100):
        clusters.extend(ecs.describe_clusters(clusters=arns[i:i + 100])["clusters"])
    clusters.sort(key=lambda c: c["clusterName"])
    return clusters


def list_services(ecs, cluster_arn):
    arns = []
    for page in ecs.get_paginator("list_services").paginate(cluster=cluster_arn):
        arns.extend(page["serviceArns"])
    return sorted(short_arn(a) for a in arns)


def list_running_tasks(ecs, cluster_arn, service_name=None):
    kwargs = {"cluster": cluster_arn, "desiredStatus": "RUNNING"}
    if service_name:
        kwargs["serviceName"] = service_name
    arns = []
    for page in ecs.get_paginator("list_tasks").paginate(**kwargs):
        arns.extend(page["taskArns"])
    tasks = []
    for i in range(0, len(arns), 100):
        tasks.extend(ecs.describe_tasks(cluster=cluster_arn, tasks=arns[i:i + 100])["tasks"])
    return tasks


def list_ec2_instances(ec2, ssm):
    """running EC2 인스턴스 목록 + SSM 관리/온라인 여부."""
    insts = []
    for page in ec2.get_paginator("describe_instances").paginate(
            Filters=[{"Name": "instance-state-name", "Values": ["running"]}]):
        for r in page.get("Reservations", []):
            for i in r.get("Instances", []):
                name = next((t["Value"] for t in i.get("Tags", []) if t["Key"] == "Name"), "")
                insts.append({
                    "id": i["InstanceId"],
                    "name": name,
                    "type": i.get("InstanceType", ""),
                    "state": i.get("State", {}).get("Name", ""),
                    "ip": i.get("PrivateIpAddress", ""),
                    "ssm": None,
                })
    online = {}
    try:
        for page in ssm.get_paginator("describe_instance_information").paginate():
            for info in page.get("InstanceInformationList", []):
                online[info.get("InstanceId")] = info.get("PingStatus")
    except (BotoCoreError, ClientError):
        pass
    for i in insts:
        i["ssm"] = online.get(i["id"])
    insts.sort(key=lambda x: (x["name"] or "").lower() or x["id"])
    return insts


def describe_service(ecs, cluster_arn, service_name):
    resp = ecs.describe_services(cluster=cluster_arn, services=[service_name])
    svcs = resp.get("services", [])
    return svcs[0] if svcs else None


def describe_services_map(ecs, cluster_arn, names):
    """service 이름 -> service dict (describe_services 는 한 번에 최대 10개)."""
    out = {}
    for i in range(0, len(names), 10):
        resp = ecs.describe_services(cluster=cluster_arn, services=names[i:i + 10])
        for s in resp.get("services", []):
            out[s["serviceName"]] = s
    return out


def get_task_definition(ecs, td_arn):
    return ecs.describe_task_definition(taskDefinition=td_arn)["taskDefinition"]


def awslogs_config(container_def):
    """taskDefinition 의 containerDefinition 에서 awslogs 설정을 뽑는다."""
    lc = container_def.get("logConfiguration") or {}
    if lc.get("logDriver") != "awslogs":
        return None
    opts = lc.get("options", {})
    return {
        "group": opts.get("awslogs-group"),
        "region": opts.get("awslogs-region"),
        "prefix": opts.get("awslogs-stream-prefix"),
    }


def service_metric(cw, metric_name, cluster_name, service_name):
    """AWS/ECS 서비스 지표(CPU/Memory Utilization %)의 최근 1시간 평균/피크."""
    now = datetime.datetime.now(datetime.timezone.utc)
    resp = cw.get_metric_statistics(
        Namespace="AWS/ECS",
        MetricName=metric_name,
        Dimensions=[
            {"Name": "ClusterName", "Value": cluster_name},
            {"Name": "ServiceName", "Value": service_name},
        ],
        StartTime=now - datetime.timedelta(hours=1),
        EndTime=now,
        Period=300,
        Statistics=["Average", "Maximum"],
    )
    dps = resp.get("Datapoints", [])
    if not dps:
        return None
    avgs = [d["Average"] for d in dps]
    maxs = [d["Maximum"] for d in dps]
    return {"avg": sum(avgs) / len(avgs), "peak": max(maxs)}


def service_metric_series(cw, metric_name, cluster_name, service_name, period=60, hours=1):
    """AWS/ECS 서비스 지표의 시계열(시간순 Average 값 리스트)."""
    now = datetime.datetime.now(datetime.timezone.utc)
    resp = cw.get_metric_statistics(
        Namespace="AWS/ECS",
        MetricName=metric_name,
        Dimensions=[
            {"Name": "ClusterName", "Value": cluster_name},
            {"Name": "ServiceName", "Value": service_name},
        ],
        StartTime=now - datetime.timedelta(hours=hours),
        EndTime=now,
        Period=period,
        Statistics=["Average"],
    )
    dps = sorted(resp.get("Datapoints", []), key=lambda d: d["Timestamp"])
    return [d["Average"] for d in dps]


def insights_metric_series(cw, metric_name, cluster_name, service_name,
                           stat="Average", period=60, hours=1):
    """ECS/ContainerInsights 서비스 지표 시계열 (network/disk 등)."""
    now = datetime.datetime.now(datetime.timezone.utc)
    resp = cw.get_metric_statistics(
        Namespace="ECS/ContainerInsights",
        MetricName=metric_name,
        Dimensions=[
            {"Name": "ClusterName", "Value": cluster_name},
            {"Name": "ServiceName", "Value": service_name},
        ],
        StartTime=now - datetime.timedelta(hours=hours),
        EndTime=now,
        Period=period,
        Statistics=[stat],
    )
    dps = sorted(resp.get("Datapoints", []), key=lambda d: d["Timestamp"])
    return [d[stat] for d in dps]


def service_autoscaling(aas, cluster_name, service_name):
    """서비스의 Application Auto Scaling 설정(min/max/정책). 없으면 None."""
    rid = f"service/{cluster_name}/{service_name}"
    tgts = aas.describe_scalable_targets(
        ServiceNamespace="ecs", ResourceIds=[rid]).get("ScalableTargets", [])
    if not tgts:
        return None
    t = tgts[0]
    pols = aas.describe_scaling_policies(
        ServiceNamespace="ecs", ResourceId=rid).get("ScalingPolicies", [])
    return {"min": t.get("MinCapacity"), "max": t.get("MaxCapacity"), "policies": pols}


def cluster_autoscaling_map(aas, cluster_name, names):
    """service 이름 -> (min, max). describe_scalable_targets 는 한 번에 최대 50개."""
    out = {}
    ids = [f"service/{cluster_name}/{n}" for n in names]
    for i in range(0, len(ids), 50):
        resp = aas.describe_scalable_targets(
            ServiceNamespace="ecs", ResourceIds=ids[i:i + 50])
        for t in resp.get("ScalableTargets", []):
            svc = t["ResourceId"].split("/")[-1]
            out[svc] = (t.get("MinCapacity"), t.get("MaxCapacity"))
    return out


def service_lb_health(elbv2, service):
    """서비스에 연결된 대상 그룹들의 (healthy, total) 합계. LB 없으면 None."""
    tg_arns = [lb["targetGroupArn"] for lb in service.get("loadBalancers", [])
               if lb.get("targetGroupArn")]
    if not tg_arns:
        return None
    healthy = total = 0
    for tg in tg_arns:
        descs = elbv2.describe_target_health(TargetGroupArn=tg)["TargetHealthDescriptions"]
        total += len(descs)
        healthy += sum(1 for d in descs if d["TargetHealth"]["State"] == "healthy")
    return (healthy, total)


def target_group_health(elbv2, tg_arn):
    """대상 그룹의 헬스 상태별 개수와 연결된 LB 정보를 반환."""
    counts = {}
    for h in elbv2.describe_target_health(TargetGroupArn=tg_arn)["TargetHealthDescriptions"]:
        st = h["TargetHealth"]["State"]
        counts[st] = counts.get(st, 0) + 1

    lb = None
    tgs = elbv2.describe_target_groups(TargetGroupArns=[tg_arn])["TargetGroups"]
    lb_arns = tgs[0].get("LoadBalancerArns", []) if tgs else []
    if lb_arns:
        lbs = elbv2.describe_load_balancers(LoadBalancerArns=lb_arns)["LoadBalancers"]
        if lbs:
            lb = {
                "name": lbs[0].get("LoadBalancerName", ""),
                "state": lbs[0].get("State", {}).get("Code", "-"),
                "dns": lbs[0].get("DNSName", ""),
            }
    return {"counts": counts, "lb": lb}

