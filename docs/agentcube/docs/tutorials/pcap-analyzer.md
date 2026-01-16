# Case Study: PCAP Analyzer

The **PCAP Analyzer** is a sophisticated example of building a multi-agent application on top of AgentCube. It uses autonomous agents to perform network forensics within an isolated sandbox.

## Architecture

The PCAP Analyzer consists of three main parts:

1. **FastAPI Server**: The user-facing API.
2. **Planner Agent**: An LLM that generates Bash scripts to analyze network traffic (`.pcap` files).
3. **AgentCube Sandbox**: The secure environment where the analysis scripts are executed.
4. **Reporter Agent**: An LLM that summarizes the execution results into a Markdown report.

## The Self-Healing Loop

A unique feature of this example is the **Automatic Repair Mechanism**:

1. **Generate**: The Planner creates an initial analysis script.
2. **Execute**: The script runs in the AgentCube sandbox.
3. **Validate**: If the script fails (e.g., due to a syntax error or missing tool), the error logs are sent back to the Planner.
4. **Repair**: The Planner analyzes the error and generates a fixed version of the script.
5. **Retry**: The process repeats until the analysis succeeds.

## How to Run It

### 1. Build the Docker Image

```bash
cd example/pcap-analyzer
docker build -t "pcap-analyzer:latest" -f Dockerfile ../../
```

### 2. Deploy to Kubernetes

You'll need an LLM API key (e.g., OpenAI or compatible):

```bash
kubectl create secret generic pcap-analyzer-secrets \
  --from-literal=openai-api-key='YOUR_API_KEY'

kubectl apply -f deployment.yaml
```

### 3. Analyze a Packet Capture

Once the service is running, you can upload a `.pcap` file for analysis:

```bash
curl -X POST "http://<POD_IP>:8000/analyze" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "pcap_file=@./mysample.pcap"
```

The response will contain the generated script, the raw logs, and the final forensic report.

## Key Takeaways

The PCAP Analyzer demonstrates:

- How to use AgentCube for **untrusted code execution**.
- How to integrate LLMs with a **sandboxed runtime**.
- The power of **self-healing** agentic workflows.

---

**Congratulations!** You've completed the AgentCube tutorials. You're now ready to build your own AI Agent platforms!
