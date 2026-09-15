# Minhas Anotações

## Tratamento de cauculos

Como no pc é usado 2 (binarios), as frações simples como 0.1 e 0.2 viram dizimas.
Para resolver isso, é melhor usar inteiros para representar valores monetários (centavos). A solução adotada por bancos mudiais (como Stripe e Nubank), é guardar dinheioro sempre em centavos inteiros (int64)
- R$ 10,50 vira 1050 centavos
- R$ 0,01 vira 1 centavo