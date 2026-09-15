#!/bin/bash
set -euo pipefail

echo "=========================================================="
echo " [LocalStack] Inicializando Filas SQS FIFO para Apostas"
echo "=========================================================="

# 1. Cria a Dead Letter Queue (DLQ FIFO)
awslocal sqs create-queue \
    --queue-name wager-transactions-dlq.fifo \
    --attributes FifoQueue=true,ContentBasedDeduplication=false

# 2. Obtém a URL e o ARN da DLQ
DLQ_URL=$(awslocal sqs get-queue-url --queue-name wager-transactions-dlq.fifo --query 'QueueUrl' --output text)
DLQ_ARN=$(awslocal sqs get-queue-attributes \
    --queue-url "$DLQ_URL" \
    --attribute-names QueueArn \
    --query 'Attributes.QueueArn' \
    --output text)

echo " [LocalStack] DLQ criada com ARN: $DLQ_ARN"

# 3. Cria a Fila Principal vinculando a DLQ com 3 tentativas máximas (RedrivePolicy)
awslocal sqs create-queue \
    --queue-name wager-transactions.fifo \
    --attributes '{
        "FifoQueue": "true",
        "ContentBasedDeduplication": "false",
        "RedrivePolicy": "{\"deadLetterTargetArn\":\"'"$DLQ_ARN"'\",\"maxReceiveCount\":\"3\"}"
    }'

echo " [LocalStack] Fila principal wager-transactions.fifo criada com sucesso!"
awslocal sqs list-queues
echo "=========================================================="
