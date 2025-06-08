# nsg-data-importer

This project hosts the implementation for nsg-data-importer which is ingesting SNMP data comming in via kafka and pushing it to Victoria Metrics. These timeseries can be later used for further analysis or event correlation.

## Deploy on Minikube
1. Image build
```
Following images are built and already pushed to github:
docker build -t nsg-data-importer:latest -f Dockerfile .
docker tag nsg-data-importer:latest dchidambaram/nsg-data-importer:latest
docker push dchidambaram/nsg-data-importer:latest
```
2. Start minikube
```
❯ minukube start

❯ minikube status
minikube
type: Control Plane
host: Running
kubelet: Running
apiserver: Running
kubeconfig: Configured
```

3. Argo CD
```
❯ kubectl create namespace argocd
❯ kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml

PortForward:
❯ kubectl get services -n argocd
❯ kubectl port-forward service/argocd-server -n argocd 8080:443

user: admin
password:
❯ kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath="{.data.password}" | base64 -d
```

4. Create Kubernetes Secrets
```
❯ kubectl create secret generic nsg-kafka-secrets \
  --from-file=dist/secrets/ca.pem \
  --from-file=dist/secrets/nsg-export.enc \
  --from-file=dist/secrets/service.cert \
  --from-file=dist/secrets/service.key
secret/nsg-kafka-secrets created
❯ kubectl get secrets
NAME                TYPE     DATA   AGE
nsg-kafka-secrets   Opaque   4      8s
```

5. Mount secrets as volume (edit deployment.yaml)
```
    spec:
      volumes:
      - name: nsg-kakfa-secret-volume
        secret:
          secretName: nsg-kakfa-secret
      containers:
      - name: nsg-data-importer
        image: dchidambaram/nsg-data-importer:latest
        imagePullPolicy: Always
        volumeMounts:
          - name: nsg-kakfa-secret-volume
            mountPath: "/etc/secrets"
            readOnly: true
```

### Deploy on Minikube
```
Login to 127.0.0.1:8080 and create application to deploy on Kubenetes (minikube)
```