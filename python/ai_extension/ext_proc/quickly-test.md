# 测试插桩

## 部署 AI Gateway
```zsh
helm upgrade -i -n kgateway-system kgateway-crds _test/kgateway-crds-1.0.0-ci1.tgz --version 1.0.0-ci1 \
          --create-namespace

helm upgrade -i -n kgateway-system kgateway _test/kgateway-1.0.0-ci1.tgz \
  --version 1.0.0-ci1 \
  --set image.registry=ghcr.io/kgateway-dev \
  --set gateway.aiExtension.enabled=true \
  --create-namespace
```

```zsh
kubectl apply -f- <<EOF
apiVersion: gateway.kgateway.dev/v1alpha1
kind: GatewayParameters
metadata:
  name: ai-gateway
  namespace: kgateway-system
  labels:
    app: ai-kgateway
spec:
  kube:
    aiExtension:
      enabled: true
      ports:
      - name: ai-monitoring
        containerPort: 9092
    service:
      type: NodePort
EOF
```

```zsh
kubectl apply -f- <<EOF
kind: Gateway
apiVersion: gateway.networking.k8s.io/v1
metadata:
  name: ai-gateway
  namespace: kgateway-system
  labels:
    app: ai-kgateway
spec:
  gatewayClassName: kgateway
  infrastructure:
    parametersRef:
      name: ai-gateway
      group: gateway.kgateway.dev
      kind: GatewayParameters
  listeners:
  - protocol: HTTP
    port: 8080
    name: http
    allowedRoutes:
      namespaces:
        from: All
EOF
```

```zsh
kubectl apply -f- <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: openrouter-secret
  namespace: kgateway-system
  labels:
    app: ai-kgateway
type: Opaque
stringData:
  Authorization: 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
--- 
apiVersion: gateway.kgateway.dev/v1alpha1
kind: Backend
metadata:
  labels:
    app: ai-kgateway
  name: openrouter
  namespace: kgateway-system
spec:
  type: AI
  ai:
    llm:
      hostOverride:
        host: openrouter.ai
        port: 443
      pathOverride:
        fullPath: "/api/v1/chat/completions"
      provider:
        openai:
          model: gpt-4o
          authToken:
            kind: SecretRef
            secretRef:
              name: openrouter-secret
--- 
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: openrouter
  namespace: kgateway-system
  labels:
    app: ai-kgateway
spec:
  parentRefs:
    - name: ai-gateway
      namespace: kgateway-system
  rules:
  - matches:
    - path:
        type: PathPrefix
        value: /openrouter
    filters:
    - type: URLRewrite
      urlRewrite:
        path:
          type: ReplaceFullPath
          replaceFullPath: /api/v1/chat/completions
    backendRefs:
    - name: openrouter
      namespace: kgateway-system
      group: gateway.kgateway.dev
      kind: Backend
EOF
```

```zsh
kubectl port-forward deployment/ai-gateway -n kgateway-system 8080:8080
```

## 部署 Tempo
```zsh
helm install tempo grafana/tempo \
  --set tempo.searchEnabled=true \
  --set tempo.target=all
```

## 部署 Grafana
```zsh
helm repo add grafana https://grafana.github.io/helm-charts

helm repo update

helm install my-grafana grafana/grafana
```

## 发送测试请求

### chat completion

```zsh
curl "localhost:8080/openrouter" \
  -H "Content-Type: application/json" \
  -d '{
  "model": "openai/gpt-4o",
  "messages": [
    {
      "role": "system",
      "content": "You are a helpful assistant that answers questions concisely."
    },
    {
      "role": "user",
      "content": "What is the meaning of life? Please elaborate in a few sentences."
    }
  ],
  "response_format": {
    "type": "text"
  },
  "n": 2,
  "seed": 12345,
  "frequency_penalty": 0.5,
  "max_tokens": 150,
  "presence_penalty": 0.3,
  "stop": ["\n\n", "END"],
  "temperature": 0.7,
  "top_k": 50,
  "top_p": 0.9
}' | jq
```

### function call

```zsh
curl "localhost:8080/openrouter" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "openai/gpt-4o",
    "messages": [
      {
        "role": "user",
        "content": "What'\''s the weather like in Singapore today, and what about in Tokyo tomorrow?"
      }
    ],
    "tools": [
      {
        "type": "function",
        "function": {
          "name": "get_current_weather",
          "description": "Get the current weather in a given location",
          "parameters": {
            "type": "object",
            "properties": {
              "location": {
                "type": "string",
                "description": "The city and state, e.g. San Francisco, CA"
              },
              "unit": {
                "type": "string",
                "enum": ["celsius", "fahrenheit"],
                "description": "The unit of temperature to return"
              }
            },
            "required": ["location"]
          }
        }
      },
      {
        "type": "function",
        "function": {
          "name": "get_future_weather",
          "description": "Get the weather forecast for a given location and date",
          "parameters": {
            "type": "object",
            "properties": {
              "location": {
                "type": "string",
                "description": "The city and state, e.g. San Francisco, CA"
              },
              "date": {
                "type": "string",
                "description": "The date for the forecast, e.g. 2025-06-30"
              },
              "unit": {
                "type": "string",
                "enum": ["celsius", "fahrenheit"],
                "description": "The unit of temperature to return"
              }
            },
            "required": ["location", "date"]
          }
        }
      }
    ],
    "tool_choice": "auto",
    "temperature": 0.5,
    "max_tokens": 200
  }' | jq
```

