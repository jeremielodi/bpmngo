package com.bpmngo.examples;

import com.fasterxml.jackson.databind.JsonNode;
import org.camunda.bpm.client.ExternalTaskClient;
import org.camunda.bpm.client.interceptor.ClientRequestInterceptor;
import org.camunda.bpm.client.interceptor.auth.BasicAuthProvider;
import org.camunda.bpm.client.topic.TopicSubscription;

import java.time.Duration;
import java.util.Map;
import java.util.Optional;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicInteger;

import static com.bpmngo.examples.Examples.check;
import static com.bpmngo.examples.Examples.waitFor;
import static com.bpmngo.examples.BpmnGoRestClient.query;

/**
 * A "charge-card" worker built on the Camunda 7 External Task Client, the
 * library a Camunda 7 Java worker already uses: point baseUrl at BpmnGo's
 * /engine-rest and add the bearer token with a request interceptor.
 *
 * The worker fails each order once (handleFailure with a retry), then
 * completes it, to show both paths. main() drives one order through.
 */
public class ExternalTaskWorkerExample {

    public static void main(String[] args) {
        BpmnGoRestClient bpmngo = BpmnGoRestClient.fromEnv();
        String baseUrl = BpmnGoRestClient.env("GOFLOW_URL", "http://127.0.0.1:8080");

        Map<String, Integer> attempts = new ConcurrentHashMap<>();
        AtomicInteger completed = new AtomicInteger();

        // /engine-rest takes HTTP Basic (BpmnGo e-mail + password) or a BpmnGo JWT.
        // Basic needs nothing more; the JWT goes through bpmngo.accessToken(),
        // which refreshes it (it only lives 15 minutes).
        ClientRequestInterceptor auth = "jwt".equals(BpmnGoRestClient.env("GOFLOW_WORKER_AUTH", "basic"))
                ? request -> request.addHeader("Authorization", "Bearer " + bpmngo.accessToken())
                : new BasicAuthProvider(BpmnGoRestClient.env("GOFLOW_USER", "admin@goflow.com"),
                        BpmnGoRestClient.env("GOFLOW_PASSWORD", "admin123"));

        ExternalTaskClient client = ExternalTaskClient.create()
                .baseUrl(baseUrl + "/engine-rest")
                .workerId("java-external-task-worker")
                .maxTasks(5)
                .asyncResponseTimeout(10_000) // long polling: BpmnGo holds fetchAndLock up to 10 s
                .addInterceptor(auth)
                .build();

        TopicSubscription subscription = client.subscribe("charge-card")
                .lockDuration(30_000)
                .variables("orderId", "amount") // fetch only these
                .handler((task, service) -> {
                    String orderId = task.getVariable("orderId");
                    int attempt = attempts.merge(task.getId(), 1, Integer::sum);
                    if (attempt == 1) {
                        // retries = what remains after this failure; 0 would raise an incident.
                        service.handleFailure(task, "card service timeout", "simulated first failure", 1, 0);
                        System.out.println("  · " + orderId + ": failed once, will retry");
                        return;
                    }
                    service.complete(task, Map.of("chargeId", "CH-" + orderId));
                    completed.incrementAndGet();
                    System.out.println("  · " + orderId + ": charged");
                })
                .open();

        try {
            bpmngo.deploy("java-order.bpmn", Examples.resource("java-order.bpmn"), "Java examples");
            String orderId = "ORDER-" + UUID.randomUUID().toString().substring(0, 8);
            String instanceId = bpmngo.post("/engine-rest/process-definition/key/JavaOrder/start", Map.of(
                    "businessKey", orderId,
                    "variables", Map.of(
                            "orderId", Map.of("value", orderId, "type", "String"),
                            "amount", Map.of("value", 75, "type", "Integer")))).path("id").asText();
            check(!instanceId.isEmpty(), "started " + orderId);

            JsonNode task = waitFor("approveOrder task", Duration.ofSeconds(10), () -> {
                JsonNode tasks = bpmngo.get("/engine-rest/task" + query("processInstanceId", instanceId));
                return tasks.size() > 0 ? Optional.of(tasks.get(0)) : Optional.empty();
            });
            bpmngo.post("/engine-rest/task/" + task.path("id").asText() + "/complete", Map.of("variables", Map.of()));

            waitFor("worker to charge the card", Duration.ofSeconds(30),
                    () -> completed.get() > 0 ? Optional.of(true) : Optional.empty());
            check(attempts.values().stream().anyMatch(n -> n == 2), "the job came back after its failure and was completed");

            waitFor("message correlation", Duration.ofSeconds(10), () -> {
                try {
                    return Optional.of(bpmngo.post("/engine-rest/message", Map.of(
                            "messageName", "PaymentConfirmed", "correlationKeys", Map.of("orderId", orderId))));
                } catch (BpmnGoRestClient.BpmnGoException e) {
                    return Optional.empty();
                }
            });
            waitFor("instance completed", Duration.ofSeconds(15), () -> "COMPLETED".equals(
                    bpmngo.get("/engine-rest/history/process-instance/" + instanceId).path("state").asText())
                    ? Optional.of(true) : Optional.empty());
            JsonNode vars = bpmngo.get("/engine-rest/process-instance/" + instanceId + "/variables");
            check(("CH-" + orderId).equals(vars.path("chargeId").path("value").asText()), "chargeId set by the worker");
            System.out.println("ExternalTaskWorkerExample OK");
        } finally {
            subscription.close();
            client.stop();
        }
    }
}
