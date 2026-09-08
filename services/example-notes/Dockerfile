FROM python:3.12-slim

WORKDIR /app
COPY server.py .

RUN useradd --no-create-home --uid 10001 notes
USER notes

EXPOSE 9001
ENV PORT=9001
CMD ["python3", "server.py"]
